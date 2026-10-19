#!/usr/bin/env python3
"""QA-only ANSI frame inspection; reuse the project's optional pyte emulator.

Read one JSON object from stdin: raw capture text and optional resize boundaries
{offset, width, height}, where offsets count UTF-8 bytes in the capture. Output
visible rows, cursor and used foreground colors. This is an emulated captured
screen, not a physical screenshot. No product state or native client is opened.
"""
import argparse
import hashlib
import importlib.metadata
import json
from pathlib import Path
import sys


def inspect(capture):
    # Embedded Python distributions may omit the script directory from sys.path.
    sys.path.insert(0, str(Path(__file__).resolve().parent))
    from render_terminal import emulator
    raw = capture['raw'].encode('utf8')
    if len(raw) > 2 * 1024 * 1024:
        raise ValueError('capture exceeds 2 MiB')
    screen, stream = emulator()
    previous = 0
    for boundary in capture.get('resizes', []):
        offset, width, height = (boundary[k] for k in ('offset', 'width', 'height'))
        if any(type(v) is not int for v in (offset, width, height)):
            raise ValueError('resize values must be integers')
        if not previous <= offset <= len(raw) or not 1 <= width <= 500 or not 1 <= height <= 200:
            raise ValueError('invalid resize boundary')
        stream.feed(raw[previous:offset].decode('utf8'))
        screen.resize(lines=height, columns=width)
        previous = offset
    stream.feed(raw[previous:].decode('utf8'))
    colors = sorted({screen.buffer[row][column].fg
                     for row in range(screen.lines) for column in range(screen.columns)
                     if screen.buffer[row][column].data.strip()
                     and screen.buffer[row][column].fg != 'default'})
    return {'rows': screen.display, 'width': screen.columns, 'height': screen.lines,
            'cursor_x': screen.cursor.x, 'cursor_y': screen.cursor.y,
            'foreground_colors': colors, 'raw_bytes': len(raw),
            'raw_sha256': hashlib.sha256(raw).hexdigest(),
            'pyte_version': importlib.metadata.version('pyte'),
            'scope': 'ANSI emulation of captured bytes; not physical pixels or human usability'}


def self_test():
    # The synthetic full frame exercises the same notice/rule/editor/footer
    # geometry as packaged checks. It validates the emulator, not the product.
    expected = {0: 'UCLI version', 1: 'read-only | effort auto | account auto',
                2: '\u2500' * 79, 18: '[Error] fixture', 19: '\u2500' * 79,
                20: '> kept', 21: '\u2500' * 79,
                22: 'Model auto | /work/framed-shell-workspace', 23: 'Tab actions'}
    raw = '\x1b[2J' + ''.join(f'\x1b[{row+1};1H{text}' for row, text in expected.items())

    def matches(frame):
        return ((frame['width'], frame['height']) == (80, 24)
                and all(frame['rows'][row].rstrip() == text for row, text in expected.items()))

    result = inspect({'raw': raw})
    assert matches(result), 'framed composer coordinates were not reconstructed'
    assert result['foreground_colors'] == []
    wrong_rule = inspect({'raw': raw + '\x1b[3;1H\x1b[2K' + '-' * 79})
    assert not matches(wrong_rule), 'discontinuous rule accepted'
    for row in expected:
        changed = inspect({'raw': raw + f'\x1b[{row+1};1H\x1b[2K'})
        assert not matches(changed), f'missing semantic row {row} was accepted'
    changed = inspect({'raw': raw + '\x1b[21;3Hmore'})
    assert changed['rows'][20].startswith('> more')
    assert changed['rows'][18].startswith('[Error]') and changed['rows'][22].startswith('Model auto')
    result = inspect({'raw': raw + '\x1b[H\x1b[31mRED\x1b[m'})
    assert 'red' in result['foreground_colors']
    result = inspect({'raw': raw + '\x1b[2J\x1b[Hsmall',
                      'resizes': [{'offset': len(raw.encode()), 'width': 60, 'height': 20}]})
    assert (result['width'], result['height']) == (60, 20) and result['rows'][0].startswith('small')
    for boundary in ({'offset': -1, 'width': 60, 'height': 20},
                     {'offset': True, 'width': 60, 'height': 20},
                     {'offset': len(raw.encode()) + 1, 'width': 60, 'height': 20}):
        try:
            inspect({'raw': raw, 'resizes': [boundary]})
        except ValueError:
            continue
        raise AssertionError('invalid boundary accepted')
    print('framed composer/footer, differential redraw, semantic-row negatives, resize/color and invalid-boundary controls passed')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--deps', type=Path)
    parser.add_argument('--self-test', action='store_true')
    args = parser.parse_args()
    if args.deps:
        sys.path.insert(0, str(args.deps.resolve(strict=True)))
    if args.self_test:
        self_test()
    else:
        sys.stdout.reconfigure(encoding='utf8')
        data = sys.stdin.buffer.read(12 * 1024 * 1024 + 1)
        if len(data) > 12 * 1024 * 1024:
            raise ValueError('JSON capture input exceeds its bound')
        print(json.dumps(inspect(json.loads(data))))


if __name__ == '__main__':
    main()
