"""Check overview data/navigation integrity; does not render or execute the UI."""
from collections import Counter
from html.parser import HTMLParser
import json
from pathlib import Path
import sys

ROOT = Path(__file__).resolve().parents[1]


class Overview(HTMLParser):
    def __init__(self):
        super().__init__()
        self.ids = []
        self.anchors = []
        self.cards = []

    def handle_starttag(self, tag, attrs):
        attrs = dict(attrs)
        if 'id' in attrs:
            self.ids.append(attrs['id'])
        href = attrs.get('href', '')
        if href.startswith('#'):
            self.anchors.append(href[1:])
        if tag == 'article' and 'pkg-card' in attrs.get('class', '').split():
            self.cards.append(attrs)

    handle_startendtag = handle_starttag


def main():
    target = Path(sys.argv[1]) if len(sys.argv) > 1 else ROOT / 'docs/overview.html'
    page = Overview()
    page.feed(target.read_text(encoding='utf-8'))
    page.close()
    tasks = json.loads((ROOT / 'planning/backlog.json').read_text(encoding='utf-8'))['tasks']
    expected = {task['id']: task for task in tasks}
    errors = []
    duplicates = [name for name, count in Counter(page.ids).items() if count > 1]
    if duplicates:
        errors.append(f'Duplicate HTML IDs: {duplicates}')
    missing = sorted(set(page.anchors) - set(page.ids))
    if missing:
        errors.append(f'Missing anchor destinations: {missing}')
    card_ids = [card.get('data-id') for card in page.cards]
    if Counter(card_ids) != Counter(expected.keys()):
        errors.append('Overview package IDs/count do not match the canonical backlog')
    for card in page.cards:
        task_id = card.get('data-id')
        if task_id not in expected:
            continue
        task = expected[task_id]
        for attr, wanted in [
            ('data-deps', task['depends_on']),
            ('data-unblocks', [other['id'] for other in tasks if task_id in other['depends_on']]),
        ]:
            actual = [part for part in card.get(attr, '').split(',') if part]
            if Counter(actual) != Counter(wanted):
                errors.append(f'{task_id}: {attr} {actual} differs from {wanted}')
        if card.get('data-phase') != str(task['phase']):
            errors.append(f'{task_id}: phase differs from canonical backlog')
    if errors:
        print('\n'.join(errors), file=sys.stderr)
        return 1
    print(f'PASS: {len(tasks)} overview packages, dependencies/reverse edges, phases, '
          f'{len(page.ids)} unique HTML IDs and {len(page.anchors)} anchor destinations')
    print('Scope: static integrity only; no browser rendering or interaction verification')
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
