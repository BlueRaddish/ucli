"""Metadata only. Invoked with -I -S -B; never import third-party packages."""
import importlib.machinery
import importlib.metadata
import json
import os
import re
import site
import sys
import sysconfig

def profile(paths):
    paths = list(dict.fromkeys(os.path.abspath(p) for p in paths if p))
    versions, locations = {}, {}
    for distribution in importlib.metadata.distributions(path=paths):
        name = re.sub(r"[-_.]+", "-", distribution.metadata.get("Name", "")).lower()
        version = distribution.version
        if not name or len(name) > 128 or len(version) > 128 or any(c in name + version for c in "\x00\r\n"):
            raise ValueError("invalid package metadata")
        if name not in versions:
            versions[name] = version
            locations[name] = os.path.abspath(str(distribution.locate_file("")))
        if len(versions) > 2048:
            raise ValueError("package metadata exceeds bound")
    modules = {}
    for name, module in (("faster-whisper", "faster_whisper"), ("ctranslate2", "ctranslate2"), ("numpy", "numpy"), ("tokenizers", "tokenizers"), ("huggingface-hub", "huggingface_hub"), ("sounddevice", "sounddevice"), ("pip", "pip")):
        spec = importlib.machinery.PathFinder.find_spec(module, paths)
        origin = os.path.abspath(spec.origin) if spec and spec.origin else ""
        location = locations.get(name, "")
        modules[name] = bool(origin and location and os.path.commonpath((origin, location)) == location)
    return {"versions": versions, "modules": modules}

paths = sysconfig.get_paths()
# Before Python3.14, -S leaves sys.prefix at the base interpreter for a venv.
# Derive only that venv's own site paths without processing pyvenv.cfg directives.
for prefix in (os.path.dirname(sys.executable), os.path.dirname(os.path.dirname(sys.executable))):
    if os.path.isfile(os.path.join(prefix, "pyvenv.cfg")):
        paths = sysconfig.get_paths(vars={"base": prefix, "platbase": prefix})
        break
global_paths = list(dict.fromkeys((paths["purelib"], paths["platlib"])))
user_site = site.getusersitepackages()
selected = sys.argv[1] if len(sys.argv) == 2 else ""
answer = {
    "format": 1,
    "python": os.path.abspath(sys.executable),
    "python_version": ".".join(str(v) for v in sys.version_info[:3]),
    "abi": sysconfig.get_config_var("SOABI") or sys.implementation.cache_tag,
    "user_site": user_site,
    "global": profile(global_paths),
    "selected": profile(([selected] if selected else [user_site]) + global_paths),
}
print(json.dumps(answer, ensure_ascii=True, separators=(",", ":")))
