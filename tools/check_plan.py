"""Validate UCLI planning contracts and render the canonical work-package index.

This checks planning artifacts, not UCLI runtime behavior.
"""
import argparse
from copy import deepcopy
import json
from pathlib import Path
import re
import sys
from urllib.parse import unquote, urlsplit

ROOT = Path(__file__).resolve().parents[1]
FIELDS = {"id", "title", "status", "phase", "size", "owner_role", "depends_on",
          "requirements", "owned_paths", "goal", "deliverables", "acceptance",
          "verification", "estimate_note"}


def validate_backlog(data):
    errors = []
    if data.get("schema_version") != 1 or data.get("project") != "ucli":
        return ["Unsupported backlog identity/schema"]
    tasks = data.get("tasks", [])
    if not tasks:
        return ["Backlog must contain tasks"]
    ids = [task.get("id") for task in tasks]
    if len(set(ids)) != len(ids):
        errors.append("Duplicate task ID")
    by_id = {task.get("id"): task for task in tasks}
    requirements = set(data.get("requirements", []))
    covered = set()
    for task in tasks:
        identity = task.get("id", "<missing>")
        if set(task) != FIELDS:
            errors.append(f"{identity}: task fields differ from contract")
        if not re.fullmatch(r"P\d{2}", str(identity)):
            errors.append(f"{identity}: invalid task ID")
        if task.get("status") not in {"proposed", "ready", "active", "blocked", "done"}:
            errors.append(f"{identity}: invalid task status")
        if task.get("size") not in {"S", "M", "L"}:
            errors.append(f"{identity}: invalid relative size")
        for field in ("title", "owner_role", "goal", "verification", "estimate_note"):
            if not isinstance(task.get(field), str) or not task[field].strip():
                errors.append(f"{identity}: empty {field}")
        for field in ("requirements", "owned_paths", "deliverables", "acceptance"):
            if not isinstance(task.get(field), list) or not task[field]:
                errors.append(f"{identity}: empty {field}")
        if len(task.get("acceptance", [])) < 2:
            errors.append(f"{identity}: needs meaningful acceptance cases")
        for path in task.get("owned_paths", []):
            if not isinstance(path, str) or path.startswith(("/", "\\")) or ":" in path or ".." in Path(path).parts:
                errors.append(f"{identity}: ownership path escapes project")
        for dependency in task.get("depends_on", []):
            if dependency not in by_id:
                errors.append(f"{identity}: unknown dependency {dependency}")
            elif dependency == identity:
                errors.append(f"{identity}: self dependency")
        for requirement in task.get("requirements", []):
            if requirement not in requirements:
                errors.append(f"{identity}: unknown requirement {requirement}")
            covered.add(requirement)
    if requirements - covered:
        errors.append("Uncovered requirements: " + ", ".join(sorted(requirements - covered)))
    visiting, visited = set(), set()

    def visit(identity):
        if identity in visiting:
            errors.append(f"Dependency cycle at {identity}")
            return
        if identity in visited or identity not in by_id:
            return
        visiting.add(identity)
        for dependency in by_id[identity].get("depends_on", []):
            visit(dependency)
        visiting.remove(identity)
        visited.add(identity)

    for identity in ids:
        visit(identity)
    return errors


def render(data):
    lines = ["# Agent work packages", "", "Generated from [backlog.json](backlog.json). "
             "Edit the JSON, then run `python tools/check_plan.py --render`.", "",
             "Package statuses are recorded below. Sizes are relative, not "
             "calendar promises. Ownership is exclusive while active; the coordinator "
             "serializes overlapping paths and shared-contract changes.", "",
             "## Assignment contract", "",
             "Give the agent its package ID, accepted contract revision, isolated "
             "worktree/branch, allowed paths, fixtures/access, acceptance cases and "
             "required evidence. The agent returns changed files, commands/exit codes, "
             "failed/skipped checks, receipts, residual risks and integration notes. "
             "It must not edit cly or shared native credentials.", "",
             "## Dependency overview", "", "| ID | Package | Phase | Size | Depends on |",
             "|---|---|---:|---|---|"]
    for task in data["tasks"]:
        deps = ", ".join(task["depends_on"]) or "none"
        lines.append(f"| {task['id']} | {task['title']} | {task['phase']} | {task['size']} | {deps} |")
    lines += ["", "## Detailed assignments", ""]
    for task in data["tasks"]:
        lines += [f"### {task['id']} — {task['title']}", "",
                  f"**Owner role:** {task['owner_role']} · **Phase:** {task['phase']} · "
                  f"**Size:** {task['size']} · **Status:** {task['status']}", "",
                  "**Prerequisites:** " + (", ".join(task["depends_on"]) or "none") + ". "
                  "**Requirements:** " + ", ".join(task["requirements"]) + ".", "",
                  task["goal"], "", "**Exclusive paths while assigned:** " +
                  ", ".join(f"`{path}`" for path in task["owned_paths"]), "",
                  "**Deliverables:** " + "; ".join(task["deliverables"]), "",
                  "**Acceptance:**", ""]
        lines.extend("- " + case for case in task["acceptance"])
        lines += ["", "**Verification:** " + task["verification"], ""]
    lines += ["## Requirement coverage", "", "| Requirement | Work packages |", "|---|---|"]
    for requirement in data["requirements"]:
        matches = [task["id"] for task in data["tasks"] if requirement in task["requirements"]]
        lines.append(f"| {requirement} | {', '.join(matches)} |")
    return "\n".join(lines) + "\n"


def check_documents(root):
    errors, count = [], 0
    docs = sorted(root.rglob("*.md"))
    for path in docs:
        text = path.read_text(encoding="utf-8")
        count += 1
        relative = path.relative_to(root)
        if "\ufffd" in text:
            errors.append(f"{relative}: replacement character")
        if len(re.findall(r"^```", text, re.M)) % 2:
            errors.append(f"{relative}: unbalanced code fence")
        # This intentionally checks destinations, not heading-slug behavior across
        # Markdown viewers. Browser/reader review verifies navigation semantics.
        without_code = re.sub(r"```[\s\S]*?```", "", text)
        for target in re.findall(r"\[[^\]]*\]\(([^)]+)\)", without_code):
            target = target.strip("<>")
            parsed = urlsplit(target)
            if parsed.scheme or target.startswith("#"):
                continue
            local = (path.parent / unquote(parsed.path)).resolve()
            if not local.exists():
                errors.append(f"{relative}: missing local link {target}")
    return errors, count


def self_test(data):
    assert not validate_backlog(data), "Baseline backlog invalid"
    broken = deepcopy(data)
    broken["tasks"][0]["depends_on"] = ["P99"]
    assert any("unknown dependency" in error for error in validate_backlog(broken))
    broken = deepcopy(data)
    broken["tasks"][0]["depends_on"] = [broken["tasks"][1]["id"]]
    broken["tasks"][1]["depends_on"] = [broken["tasks"][0]["id"]]
    assert any("cycle" in error for error in validate_backlog(broken))
    broken = deepcopy(data)
    broken["tasks"].append(deepcopy(broken["tasks"][0]))
    assert any("Duplicate" in error for error in validate_backlog(broken))
    broken = deepcopy(data)
    broken["requirements"].append("R99")
    assert any("Uncovered" in error for error in validate_backlog(broken))
    broken = deepcopy(data)
    broken["tasks"][0]["owned_paths"] = ["../cly/bin/cly"]
    assert any("escapes project" in error for error in validate_backlog(broken))
    broken = deepcopy(data)
    broken["tasks"][0]["acceptance"] = []
    assert any("acceptance" in error for error in validate_backlog(broken))
    print("PASS: baseline plus six negative backlog controls")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--self-test", action="store_true")
    parser.add_argument("--render", action="store_true")
    args = parser.parse_args()
    data = json.loads((ROOT / "planning/backlog.json").read_text(encoding="utf-8"))
    errors = validate_backlog(data)
    if errors:
        print("\n".join(errors), file=sys.stderr)
        return 1
    if args.self_test:
        self_test(data)
        return 0
    expected = render(data)
    target = ROOT / "planning/work-packages.md"
    if args.render:
        target.write_text(expected, encoding="utf-8", newline="\n")
        print(f"Rendered {len(data['tasks'])} work packages")
        return 0
    if not target.exists() or target.read_text(encoding="utf-8") != expected:
        errors.append("Work-package rendering differs from canonical backlog; run --render")
    document_errors, count = check_documents(ROOT)
    errors.extend(document_errors)
    if errors:
        print("\n".join(errors), file=sys.stderr)
        return 1
    print(f"PASS: {len(data['tasks'])} tasks, acyclic dependencies, "
          f"{len(data['requirements'])} covered requirements, {count} Markdown files and local links")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
