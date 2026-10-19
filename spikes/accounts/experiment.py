"""P04 synthetic boundary experiment; no native credentials, providers or keyrings."""

import argparse
from contextlib import contextmanager
import hashlib
import json
from pathlib import Path
import sqlite3
import sys
import uuid


class Rejected(Exception):
    def __init__(self, code, reason, **details):
        self.code = code
        self.receipt = {"accepted": False, "reason": reason, **details}


def emit(receipt):
    print(json.dumps({"fixture_only": True, **receipt}, sort_keys=True), flush=True)


def connect(path):
    db = sqlite3.connect(path, timeout=3, isolation_level=None)
    db.row_factory = sqlite3.Row
    db.execute("PRAGMA foreign_keys=ON")
    db.execute("PRAGMA busy_timeout=3000")
    return db


@contextmanager
def transaction(db):
    db.execute("BEGIN IMMEDIATE")
    try:
        yield
        db.execute("COMMIT")
    except BaseException:
        db.execute("ROLLBACK")
        raise


def initialize(path, fixture_path, profile_root):
    if path.exists():
        raise Rejected(2, "fixture database already exists; choose a fresh path")
    fixture = json.loads(fixture_path.read_text(encoding="utf-8"))
    if fixture.get("fixture_only") is not True:
        raise Rejected(2, "only synthetic fixture input is accepted")
    path.parent.mkdir(parents=True, exist_ok=True)
    with connect(path) as db:
        db.executescript("""
            CREATE TABLE owners(handle TEXT PRIMARY KEY, subject TEXT NOT NULL,
                generation INTEGER NOT NULL, value TEXT NOT NULL);
            CREATE TABLE pools(id TEXT PRIMARY KEY, remaining INTEGER,
                units TEXT NOT NULL);
            CREATE TABLE connections(id TEXT PRIMARY KEY, owner TEXT NOT NULL REFERENCES owners,
                subject TEXT NOT NULL, pool TEXT NOT NULL REFERENCES pools);
            CREATE TABLE shared_backend(singleton INTEGER PRIMARY KEY CHECK(singleton=1),
                owner TEXT NOT NULL REFERENCES owners);
            CREATE TABLE refresh_leases(owner TEXT PRIMARY KEY REFERENCES owners,
                epoch INTEGER NOT NULL, nonce TEXT NOT NULL, expires INTEGER NOT NULL);
            CREATE TABLE reservations(attempt TEXT PRIMARY KEY, connection TEXT NOT NULL REFERENCES connections,
                pool TEXT NOT NULL REFERENCES pools, units INTEGER NOT NULL CHECK(units>0),
                status TEXT NOT NULL CHECK(status IN ('held','uncertain','released')),
                expires INTEGER NOT NULL, proof TEXT);
            CREATE TABLE dispatches(id INTEGER PRIMARY KEY, connection TEXT NOT NULL,
                actual_subject TEXT NOT NULL, guarded INTEGER NOT NULL);
        """)
        with transaction(db):
            for owner in fixture["owners"]:
                db.execute("INSERT INTO owners VALUES(?,?,1,?)",
                           (owner["handle"], owner["subject"], "SYNTHETIC-ONLY-initial-" + owner["handle"]))
            for pool in fixture["pools"]:
                db.execute("INSERT INTO pools VALUES(?,?,?)", (pool["id"], pool["remaining"], pool["units"]))
            for connection in fixture["connections"]:
                db.execute("INSERT INTO connections VALUES(?,?,?,?)",
                           (connection["id"], connection["owner"], connection["subject"], connection["pool"]))
            db.execute("INSERT INTO shared_backend VALUES(1,?)", (fixture["shared_backend_owner"],))
    for label, owner in zip(("home-a", "home-b"), fixture["owners"]):
        directory = profile_root / label
        directory.mkdir(parents=True, exist_ok=True)
        (directory / "profile.json").write_text(json.dumps({"fixture_only": True, "owner": owner["handle"]}), encoding="utf-8")
    return {"accepted": True, "reason": "synthetic stores initialized", "profiles": ["home-a", "home-b"]}


def row_required(db, table, field, value):
    # Identifiers come only from fixed internal calls; values remain parameterized.
    row = db.execute(f"SELECT * FROM {table} WHERE {field}=?", (value,)).fetchone()
    if row is None:
        raise Rejected(3, "unknown fixture reference", kind=table, reference=value)
    return row


def probe(db, connection_id, profile, backend, guarded=True):
    selected = row_required(db, "connections", "id", connection_id)
    config = json.loads((profile / "profile.json").read_text(encoding="utf-8"))
    if config.get("fixture_only") is not True:
        raise Rejected(2, "not a synthetic profile")
    owner = (db.execute("SELECT owner FROM shared_backend WHERE singleton=1").fetchone()["owner"]
             if backend == "shared-global" else config["owner"])
    observed = row_required(db, "owners", "handle", owner)
    if guarded and (owner != selected["owner"] or observed["subject"] != selected["subject"]):
        raise Rejected(3, "identity/owner mismatch; dispatch blocked", selected=selected["subject"],
                       observed=observed["subject"], backend=backend, dispatched=False)
    db.execute("INSERT INTO dispatches(connection,actual_subject,guarded) VALUES(?,?,?)",
               (connection_id, observed["subject"], int(guarded)))
    return {"accepted": True, "backend": backend, "selected": selected["subject"],
            "observed": observed["subject"], "owner": owner, "dispatched": True,
            "intentionally_unsafe": not guarded}


def acquire_refresh(db, owner, now, ttl):
    with transaction(db):
        credential = row_required(db, "owners", "handle", owner)
        previous = db.execute("SELECT * FROM refresh_leases WHERE owner=?", (owner,)).fetchone()
        if previous and previous["expires"] > now:
            raise Rejected(4, "refresh already owned", owner=owner, expires=previous["expires"])
        epoch = previous["epoch"] + 1 if previous else 1
        nonce = uuid.uuid4().hex
        db.execute("INSERT INTO refresh_leases VALUES(?,?,?,?) ON CONFLICT(owner) DO UPDATE SET "
                   "epoch=excluded.epoch,nonce=excluded.nonce,expires=excluded.expires",
                   (owner, epoch, nonce, now + ttl))
        return {"owner": owner, "generation": credential["generation"],
                "epoch": epoch, "nonce": nonce, "expires": now + ttl}


def synthetic_value(tag):
    return "SYNTHETIC-ONLY-refresh-" + tag


def commit_refresh(db, lease, now, tag):
    with transaction(db):
        current = row_required(db, "owners", "handle", lease["owner"])
        held = row_required(db, "refresh_leases", "owner", lease["owner"])
        if (held["nonce"] != lease["nonce"] or held["epoch"] != lease["epoch"]
                or held["expires"] <= now or current["generation"] != lease["generation"]):
            raise Rejected(3, "stale refresh rejected", owner=lease["owner"],
                           observed_generation=current["generation"], stale_generation=lease["generation"])
        db.execute("UPDATE owners SET generation=generation+1,value=? WHERE handle=?",
                   (synthetic_value(tag), lease["owner"]))
        db.execute("UPDATE refresh_leases SET expires=? WHERE owner=?", (now, lease["owner"]))
        return {"accepted": True, "reason": "refresh committed", "owner": lease["owner"],
                "generation": current["generation"] + 1, "epoch": held["epoch"]}


def reserve(db, connection_id, attempt, units, now, ttl):
    if units <= 0 or ttl <= 0:
        raise Rejected(2, "units and ttl must be positive")
    # The local fixture knows exact synthetic units. Production subscription
    # percentages require conservative ranges and provider-specific reconciliation.
    with transaction(db):
        selected = row_required(db, "connections", "id", connection_id)
        pool = row_required(db, "pools", "id", selected["pool"])
        db.execute("UPDATE reservations SET status='uncertain' WHERE status='held' AND expires<=?", (now,))
        old = db.execute("SELECT * FROM reservations WHERE attempt=?", (attempt,)).fetchone()
        if old:
            if old["connection"] != connection_id or old["units"] != units:
                raise Rejected(2, "attempt id reused with different reservation intent")
            return {"accepted": True, "reason": "existing reservation receipt", "duplicate": True,
                    "attempt": attempt, "pool": old["pool"], "status": old["status"]}
        active = db.execute("SELECT COUNT(*) count,COALESCE(SUM(units),0) units FROM reservations "
                            "WHERE pool=? AND status IN ('held','uncertain')", (pool["id"],)).fetchone()
        if pool["remaining"] is None and active["count"] >= 1:
            # Commit expired->uncertain classification even when admission rejects.
            rejection = Rejected(4, "unknown pool already has in-flight or uncertain work", pool=pool["id"])
        elif pool["remaining"] is not None and active["units"] + units > pool["remaining"]:
            rejection = Rejected(4, "shared pool capacity unavailable", pool=pool["id"])
        else:
            db.execute("INSERT INTO reservations VALUES(?,?,?,?, 'held',?,NULL)",
                       (attempt, connection_id, pool["id"], units, now + ttl))
            return {"accepted": True, "reason": "reservation created", "attempt": attempt,
                    "pool": pool["id"], "status": "held", "capacity_known": pool["remaining"] is not None}
    raise rejection


def reconcile(db, attempt, proof, remaining=None):
    with transaction(db):
        old = row_required(db, "reservations", "attempt", attempt)
        if old["status"] == "released":
            return {"accepted": True, "attempt": attempt, "status": "released",
                    "duplicate": True, "proof": old["proof"]}
        if proof not in ("confirmed-unsent", "confirmed-stopped", "confirmed-completed"):
            db.execute("UPDATE reservations SET status='uncertain' WHERE attempt=? AND status!='released'", (attempt,))
            return {"accepted": True, "attempt": attempt, "status": "uncertain",
                    "reason": "cancel/expiry alone does not release capacity"}
        pool = row_required(db, "pools", "id", old["pool"])
        if pool["remaining"] is not None and proof != "confirmed-unsent" and remaining is None:
            raise Rejected(5, "stopped/completed known quota needs a fresh synthetic remaining observation")
        if remaining is not None:
            if remaining < 0:
                raise Rejected(2, "remaining observation cannot be negative")
            db.execute("UPDATE pools SET remaining=? WHERE id=?", (remaining, old["pool"]))
        db.execute("UPDATE reservations SET status='released',proof=? WHERE attempt=?", (proof, attempt))
        return {"accepted": True, "attempt": old["attempt"], "status": "released", "proof": proof,
                "reason": "synthetic external outcome reconciled"}


def snapshot(db):
    owners = []
    for row in db.execute("SELECT * FROM owners ORDER BY handle"):
        owners.append({"owner": row["handle"], "subject": row["subject"], "generation": row["generation"],
                       "value_sha256": hashlib.sha256(row["value"].encode()).hexdigest()})
    return {"accepted": True, "owners": owners,
            "reservations": [dict(row) for row in db.execute("SELECT * FROM reservations ORDER BY attempt")],
            "dispatches": [dict(row) for row in db.execute("SELECT * FROM dispatches ORDER BY id")],
            "sqlite_version": sqlite3.sqlite_version,
            "journal_mode": db.execute("PRAGMA journal_mode").fetchone()[0]}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--db", type=Path, required=True)
    commands = parser.add_subparsers(dest="action", required=True)
    init = commands.add_parser("init")
    init.add_argument("--fixture", type=Path, required=True)
    init.add_argument("--profiles", type=Path, required=True)
    for name in ("probe", "unsafe-probe"):
        item = commands.add_parser(name)
        item.add_argument("--connection", required=True)
        item.add_argument("--profile", type=Path, required=True)
        item.add_argument("--backend", choices=("shared-global", "isolated-owner"), required=True)
    refresh = commands.add_parser("refresh-worker")
    refresh.add_argument("--owner", required=True)
    refresh.add_argument("--start-now", type=int, required=True)
    refresh.add_argument("--finish-now", type=int, required=True)
    refresh.add_argument("--ttl", type=int, default=10)
    refresh.add_argument("--tag", required=True)
    unsafe = commands.add_parser("unsafe-refresh")
    unsafe.add_argument("--owner", required=True)
    unsafe.add_argument("--tag", required=True)
    reservation = commands.add_parser("reserve")
    reservation.add_argument("--connection", required=True)
    reservation.add_argument("--attempt", required=True)
    reservation.add_argument("--units", type=int, default=6)
    reservation.add_argument("--now", type=int, required=True)
    reservation.add_argument("--ttl", type=int, default=10)
    reconciliation = commands.add_parser("reconcile")
    reconciliation.add_argument("--attempt", required=True)
    reconciliation.add_argument("--proof", choices=("cancel-request", "expiry", "confirmed-unsent",
                                                     "confirmed-stopped", "confirmed-completed"), required=True)
    reconciliation.add_argument("--remaining", type=int)
    commands.add_parser("inspect")
    args = parser.parse_args()
    try:
        if args.action == "init":
            emit(initialize(args.db, args.fixture, args.profiles))
            return 0
        if not args.db.is_file():
            raise Rejected(3, "initialize a fresh synthetic database first")
        with connect(args.db) as db:
            if args.action in ("probe", "unsafe-probe"):
                result = probe(db, args.connection, args.profile, args.backend, args.action == "probe")
            elif args.action == "refresh-worker":
                if args.ttl <= 0 or args.finish_now < args.start_now:
                    raise Rejected(2, "invalid synthetic lease clock")
                lease = acquire_refresh(db, args.owner, args.start_now, args.ttl)
                emit({"event": "lease-acquired", "owner": args.owner, "generation": lease["generation"],
                      "epoch": lease["epoch"], "expires": lease["expires"]})
                if sys.stdin.readline().strip() != "commit":
                    raise Rejected(5, "no commit instruction; lease remains bounded by expiry")
                result = commit_refresh(db, lease, args.finish_now, args.tag)
            elif args.action == "unsafe-refresh":
                row_required(db, "owners", "handle", args.owner)
                db.execute("UPDATE owners SET generation=generation+1,value=? WHERE handle=?",
                           (synthetic_value(args.tag), args.owner))
                result = {"accepted": True, "intentionally_unsafe": True,
                          "reason": "negative control overwrote credential without fencing"}
            elif args.action == "reserve":
                result = reserve(db, args.connection, args.attempt, args.units, args.now, args.ttl)
            elif args.action == "reconcile":
                result = reconcile(db, args.attempt, args.proof, args.remaining)
            else:
                result = snapshot(db)
            emit(result)
        return 0
    except Rejected as exc:
        emit(exc.receipt)
        return exc.code
    except (OSError, ValueError, KeyError, TypeError, sqlite3.Error) as exc:
        emit({"accepted": False, "reason": "fixture error", "detail": str(exc)})
        return 9


if __name__ == "__main__":
    raise SystemExit(main())
