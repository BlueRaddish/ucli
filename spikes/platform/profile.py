"""Record service stage timings when a fixture overhead gate fails."""
import argparse
import hashlib
import json
import tempfile
from pathlib import Path
from verify import config, run, objects

p = argparse.ArgumentParser()
p.add_argument("--binary", type=Path, required=True)
p.add_argument("--evidence", type=Path, required=True)
args = p.parse_args()
binary = args.binary.resolve()
with tempfile.TemporaryDirectory(prefix="ucli-p01-profile-") as tmp:
    root = config(Path(tmp))
    rows = [objects(run([binary, "run", "--root", root, "--id", str(i)])[1])[-1] for i in range(5)]
receipt = {"binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(), "samples": rows}
args.evidence.parent.mkdir(parents=True, exist_ok=True)
args.evidence.write_text(json.dumps(receipt, indent=2), encoding="utf-8")
print(json.dumps(receipt, indent=2))
