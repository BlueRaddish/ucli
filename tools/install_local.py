"""Install a verified local executable, or replace one bound to an old receipt.

--replace-owned OLD_RECEIPT authorizes only that destination and its exact old
bytes. Backups and immutable transition records are retained beside it. A retry
after swap-before-proof needs the same old receipt and new build/acceptance proof.

Scope: cooperating installers, local filesystems and process crashes. The OS lock
file is deliberately never removed. This is not protection against arbitrary
noncooperating filesystem writers or power loss. On POSIX a running old executable
may keep its old inode; Windows sharing/in-use errors leave the old file intact.
No PATH, other CLI, account, remote channel or automatic backup cleanup is changed.
"""
import argparse
from contextlib import contextmanager
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import sys
import tempfile

LOCK_CONTENT=b'UCLI-INSTALL-LOCK-v1\n'
FLAGS=subprocess.CREATE_NO_WINDOW if os.name=='nt' else 0

def sha(path):
    h=hashlib.sha256()
    with path.open('rb') as f:
        while chunk:=f.read(1024*1024): h.update(chunk)
    return h.hexdigest()

def regular(path,missing=False):
    try: info=path.lstat()
    except FileNotFoundError:
        if missing: return None
        raise
    if (not stat.S_ISREG(info.st_mode) or info.st_nlink!=1 or
        getattr(info,'st_file_attributes',0)&getattr(stat,'FILE_ATTRIBUTE_REPARSE_POINT',0x400)):
        raise ValueError(f'Refusing nonregular, linked or aliased file: {path}')
    return info

def canonical(path):
    # Refuse leaf links before resolving existing regular files (including
    # Windows short names), so cooperating aliases share one lock identity.
    path=Path(path)
    existing=regular(path,missing=True)
    if existing is None and os.name=='nt' and path.name.endswith(('.', ' ')):
        raise ValueError('New Windows file names cannot end in a dot or space.')
    path=Path(os.path.abspath(path))
    if existing is not None: return path.resolve(strict=True)
    return path.parent.resolve()/path.name

def read_json(path):
    info=regular(path)
    if info.st_size>16*1024*1024: raise ValueError('Receipt exceeds its 16 MiB bound.')
    raw=path.read_bytes()
    def unique(pairs):
        result={}
        for key,value in pairs:
            if key in result: raise ValueError('Duplicate receipt field.')
            result[key]=value
        return result
    result=json.loads(raw,object_pairs_hook=unique)
    if not isinstance(result,dict): raise ValueError('Receipt must be an object.')
    return result,hashlib.sha256(raw).hexdigest()

def require_hash(path,digest):
    regular(path)
    if sha(path)!=digest: raise ValueError(f'File changed or belongs to another installation: {path}')

def distinct(paths):
    if len(set(paths))!=len(paths): raise ValueError('Executable, receipt and installer control paths must be distinct.')
    for path in paths: regular(path,missing=True)

def fresh_evidence(path,protected):
    if path in protected or path.exists() or path.is_symlink():
        raise ValueError('Evidence must be a fresh path distinct from all executable, receipt, backup and control files.')

@contextmanager
def destination_lock(path):
    regular(path,missing=True)
    fd=os.open(path,os.O_RDWR|os.O_CREAT|getattr(os,'O_NOFOLLOW',0),0o600)
    acquired=False
    try:
        os.set_inheritable(fd,False)
        if not stat.S_ISREG(os.fstat(fd).st_mode) or os.fstat(fd).st_nlink!=1:
            raise ValueError('Installer lock is linked or not a regular file.')
        if os.name=='nt':
            import msvcrt
            msvcrt.locking(fd,msvcrt.LK_NBLCK,1)
        else:
            import fcntl
            fcntl.flock(fd,fcntl.LOCK_EX|fcntl.LOCK_NB)
        acquired=True
        os.lseek(fd,0,os.SEEK_SET)
        content=os.read(fd,len(LOCK_CONTENT)+1)
        if content not in (b'',LOCK_CONTENT): raise ValueError('Refusing an unrecognized installer lock file.')
        if not content:
            os.lseek(fd,0,os.SEEK_SET); os.write(fd,LOCK_CONTENT); os.fsync(fd)
        yield
    finally:
        try:
            if acquired:
                if os.name=='nt':
                    os.lseek(fd,0,os.SEEK_SET); msvcrt.locking(fd,msvcrt.LK_UNLCK,1)
                else: fcntl.flock(fd,fcntl.LOCK_UN)
        finally: os.close(fd)
        # Never unlink: replacing a locked inode would permit a second lock owner.

def stage_copy(source,parent,digest):
    staged=None
    try:
        with tempfile.NamedTemporaryFile(prefix='.ucli-stage-',dir=parent,delete=False) as f:
            staged=Path(f.name)
            with source.open('rb') as src:
                while chunk:=src.read(1024*1024): f.write(chunk)
            f.flush(); os.fsync(f.fileno())
        staged.chmod(0o755)
        require_hash(staged,digest)
        return staged
    except BaseException:
        if staged is not None: staged.unlink(missing_ok=True)
        raise

def publish_new(staged,target,cooperative=False):
    # Control records share the destination's lock. Rename avoids a crash leaving
    # an extra staging hard link that would make the retained backup ambiguous.
    # Arbitrary noncooperating races are explicitly outside this local tool's scope.
    # Evidence uses exclusive link creation even across different destination locks.
    try:
        if cooperative:
            if target.exists() or target.is_symlink(): raise FileExistsError(str(target))
            os.rename(staged,target)
        else: os.link(staged,target)
    finally: staged.unlink(missing_ok=True)

def publish_json(target,body,cooperative=False):
    staged=None
    try:
        with tempfile.NamedTemporaryFile(prefix='.ucli-proof-',dir=target.parent,delete=False) as f:
            staged=Path(f.name)
            f.write((json.dumps(body,indent=2)+'\n').encode('utf-8')); f.flush(); os.fsync(f.fileno())
        publish_new(staged,target,cooperative)
    finally:
        if staged is not None: staged.unlink(missing_ok=True)

def verify_version(destination):
    checked=subprocess.run([str(destination),'--version'],stdin=subprocess.DEVNULL,capture_output=True,
        timeout=5,creationflags=FLAGS)
    if checked.returncode or not checked.stdout.startswith(b'ucli '):
        raise RuntimeError('Installed version check failed.')
    return checked.stdout.decode('utf-8').strip()

def restore_backup(destination,backup,old_digest,new_digest):
    require_hash(backup,old_digest)
    staged=stage_copy(backup,destination.parent,old_digest)
    try:
        require_hash(destination,new_digest)
        os.replace(staged,destination)
        require_hash(destination,old_digest)
        verify_version(destination)
    finally: staged.unlink(missing_ok=True)

def install(args):
    source,destination,build_path,acceptance_path,evidence=[canonical(p) for p in
        (args.binary,args.destination,args.build_receipt,args.acceptance_receipt,args.evidence)]
    old_path=canonical(args.replace_owned) if args.replace_owned else None
    lock=destination.with_name('.'+destination.name+'.ucli-install.lock')
    protected=[source,destination,build_path,acceptance_path,lock]+([old_path] if old_path else [])
    distinct(protected); fresh_evidence(evidence,protected)
    regular(source); digest=sha(source)
    build,build_hash=read_json(build_path); accepted,acceptance_hash=read_json(acceptance_path)
    if (type(build.get('exit_code')) is not int or build['exit_code']!=0 or
        not isinstance(build.get('binaries'),list) or
        not any(isinstance(b,dict) and b.get('sha256')==digest for b in build['binaries'])):
        raise ValueError('Binary is absent from a successful build receipt.')
    if (type(accepted.get('failed')) is not int or accepted['failed']!=0 or
        type(accepted.get('passed')) is not int or accepted['passed']<=0 or accepted.get('binary_sha256')!=digest):
        raise ValueError('This exact binary needs a successful packaged acceptance receipt.')
    old_digest=old_hash=None; backup=journal=None; transition=None
    if old_path:
        old,old_hash=read_json(old_path)
        old_digest=old.get('sha256','')
        if (type(old.get('exit_code')) is not int or old['exit_code']!=0 or
            not isinstance(old.get('destination'),str) or not Path(old['destination']).is_absolute() or
            canonical(Path(old['destination']))!=destination or not isinstance(old_digest,str) or
            not re.fullmatch('[0-9a-f]{64}',old_digest) or not isinstance(old.get('version'),str) or not old['version'].startswith('ucli ')):
            raise ValueError('Old receipt does not bind a verified executable to this exact destination.')
        backup=destination.with_name('.'+destination.name+'.ucli-backup-'+old_digest)
        journal=destination.with_name('.'+destination.name+'.ucli-transition-'+old_digest+'-'+digest+'.json')
        protected.extend((backup,journal))
        distinct(protected); fresh_evidence(evidence,protected)
        transition={'version':1,'kind':'ucli-owned-replacement','destination':str(destination),
            'old_sha256':old_digest,'new_sha256':digest,'old_receipt_sha256':old_hash,'backup':str(backup)}
    destination.parent.mkdir(parents=True,exist_ok=True)
    evidence.parent.mkdir(parents=True,exist_ok=True)
    with destination_lock(lock):
        # Repeat checks under the cooperating-installers lock. It cannot provide
        # a filesystem CAS against arbitrary noncooperating writers.
        distinct(protected); fresh_evidence(evidence,protected)
        current=sha(destination) if regular(destination,missing=True) else None
        replaced=False
        if old_path:
            if current==old_digest==digest:
                action='verified owned unchanged executable'
            elif current not in (old_digest,digest):
                raise ValueError('Owned destination is missing or changed; no replacement performed.')
            else:
                if current==old_digest:
                    if backup.exists(): require_hash(backup,old_digest)
                    else: publish_new(stage_copy(destination,destination.parent,old_digest),backup,cooperative=True)
                    if journal.exists():
                        if read_json(journal)[0]!=transition: raise ValueError('Transition journal does not match this replacement.')
                    else: publish_json(journal,transition,cooperative=True)
                    staged=stage_copy(source,destination.parent,digest)
                    try:
                        require_hash(destination,old_digest)
                        os.replace(staged,destination)
                    finally: staged.unlink(missing_ok=True)
                    action='replaced owned executable'
                else:
                    # Desired bytes alone do not prove ownership after a crash.
                    # Require the exact prepared transition and old verified backup.
                    if read_json(journal)[0]!=transition: raise ValueError('No matching prepared replacement establishes ownership.')
                    require_hash(backup,old_digest)
                    action='recovered owned replacement proof'
                replaced=True
        elif current is None:
            publish_new(stage_copy(source,destination.parent,digest),destination)
            action='installed at new path'
        elif current==digest:
            action='verified existing identical executable'
        else:
            raise ValueError('Destination already contains another file; no overwrite performed.')
        try:
            require_hash(destination,digest)
            version=verify_version(destination)
            require_hash(destination,digest)
        except (OSError,ValueError,RuntimeError,subprocess.TimeoutExpired) as error:
            if replaced:
                try: restore_backup(destination,backup,old_digest,digest)
                except Exception as rollback_error:
                    raise RuntimeError(f'New version check failed ({error}); rollback not confirmed ({rollback_error}). Verified backup retained at {backup}.') from error
                raise RuntimeError(f'New version check failed ({error}); verified old executable restored. Backup retained at {backup}.') from error
            raise RuntimeError(f'Installed version check failed; no successful proof was written: {error}') from error
        receipt={'action':action,'source':str(source),'destination':str(destination),'sha256':digest,
            'build_receipt':str(build_path),'build_receipt_sha256':build_hash,
            'acceptance_receipt':str(acceptance_path),'acceptance_receipt_sha256':acceptance_hash,
            'version':version,'exit_code':0,'installer_version':2,
            'scope':'explicit local executable; cooperative OS lock and process-crash recovery only; no PATH, remote update or power-loss guarantee'}
        if old_path:
            receipt.update(old_receipt=str(old_path),old_receipt_sha256=old_hash,old_sha256=old_digest)
            if replaced: receipt.update(backup=str(backup),transition=str(journal))
        publish_json(evidence,receipt)
        return receipt

def main(argv=None):
    p=argparse.ArgumentParser(description=__doc__)
    for name in ('binary','destination','build-receipt','acceptance-receipt','evidence'):
        p.add_argument('--'+name,type=Path,required=True)
    p.add_argument('--replace-owned',type=Path,metavar='OLD_RECEIPT')
    args=p.parse_args(argv)
    try: receipt=install(args)
    except (ValueError,FileNotFoundError) as error: p.error(str(error))
    except (OSError,RuntimeError,subprocess.TimeoutExpired) as error:
        print(str(error),file=sys.stderr); return 1
    print(json.dumps(receipt,indent=2)); return 0

if __name__=='__main__': raise SystemExit(main())
