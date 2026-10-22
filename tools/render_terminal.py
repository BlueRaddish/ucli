#!/usr/bin/env python3
"""Render a recorded ConPTY observation for visual QA, never as a desktop screenshot.

Optional QA-only dependencies: pyte==0.8.2 and Pillow. Pass --deps for an isolated
pyte installation. This tool is not included in the shipped UCLI executable.
"""
import argparse
import hashlib
import importlib.metadata
import json
from pathlib import Path
import sys


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def emulator():
    import pyte

    class Screen(pyte.Screen):
        def window_resize(self, operation=0, *args, **kwargs):
            if operation == 8 and len(args) >= 2:
                height, width = args[:2]
                if 1 <= height <= 200 and 1 <= width <= 500:
                    self.resize(lines=height, columns=width)

    class Stream(pyte.Stream):
        csi = {**pyte.Stream.csi, 't': 'window_resize'}
        events = pyte.Stream.events | {'window_resize'}

    screen = Screen(80, 24)
    return screen, Stream(screen)


def replay_capture(raw, resizes=()):
    """Replay exactly captured bytes, applying validated out-of-band resizes."""
    if not isinstance(raw, bytes) or len(raw) > 2 * 1024 * 1024:
        raise ValueError('capture must be bytes within 2 MiB')
    if not isinstance(resizes, (list, tuple)):
        raise ValueError('resize boundaries must be a list')
    screen, stream = emulator()
    previous = 0
    for boundary in resizes:
        if not isinstance(boundary, dict) or not all(k in boundary for k in ('offset', 'width', 'height')):
            raise ValueError('invalid resize boundary')
        offset, width, height = (boundary[k] for k in ('offset', 'width', 'height'))
        if any(type(v) is not int for v in (offset, width, height)):
            raise ValueError('resize values must be integers')
        if not previous <= offset <= len(raw) or not 1 <= width <= 500 or not 1 <= height <= 200:
            raise ValueError('invalid resize boundary')
        # Strict decoding also rejects a boundary in the middle of a UTF-8 glyph.
        stream.feed(raw[previous:offset].decode('utf8'))
        screen.resize(lines=height, columns=width)
        previous = offset
    stream.feed(raw[previous:].decode('utf8'))
    return screen


def verified_capture(raw, offset, proof):
    """Bind a rendered observation to its saved, already-asserted frame."""
    if not isinstance(proof, dict) or not isinstance(proof.get('frame'), dict):
        raise ValueError('saved frame metadata is missing')
    frame = proof['frame']
    if type(offset) is not int or not 0 < offset <= len(raw):
        raise ValueError('invalid observation offset')
    if type(proof.get('observed_offset')) is not int or any(
            type(frame.get(k)) is not int for k in ('raw_bytes', 'width', 'height', 'cursor_x', 'cursor_y')):
        raise ValueError('saved frame coordinates and offsets must be integers')
    if proof.get('observed_offset') != offset or frame.get('raw_bytes') != offset:
        raise ValueError('observation and saved frame offsets differ')
    prefix = raw[:offset]
    if frame.get('raw_sha256') != hashlib.sha256(prefix).hexdigest():
        raise ValueError('saved frame does not match the captured byte prefix')
    screen = replay_capture(prefix, proof.get('resizes', []))
    actual = (screen.columns, screen.lines, screen.display, screen.cursor.x, screen.cursor.y)
    expected = (frame.get('width'), frame.get('height'), frame.get('rows'), frame.get('cursor_x'), frame.get('cursor_y'))
    if actual != expected:
        raise ValueError('replayed dimensions, rows or cursor differ from the verified frame')
    if 'style_spans' in frame and frame['style_spans'] != style_spans(screen):
        raise ValueError('replayed foreground/background/reverse spans differ from the verified frame')
    return screen


def style_spans(screen):
    """Run-length styles include blank cells, so full-row bands are testable."""
    spans = []
    for row in range(screen.lines):
        start, previous = 0, None
        for column in range(screen.columns + 1):
            cell = screen.buffer[row][column] if column < screen.columns else None
            current = (cell.fg, cell.bg, cell.reverse) if cell else None
            if current != previous:
                if previous is not None:
                    spans.append({'row': row, 'start': start, 'end': column,
                                  'foreground': previous[0], 'background': previous[1],
                                  'reverse': previous[2]})
                start, previous = column, current
    return spans


def self_test():
    screen, stream = emulator()
    stream.feed('\x1b[2J\x1b[Habc\x1b[2;1HX')
    assert screen.display[0].startswith('abc') and screen.display[1].startswith('X')
    stream.feed('\x1b[8;20;60t')
    assert (screen.columns, screen.lines) == (60, 20)
    stream.feed('\x1b[H\x1b[2K\u4e2d!')
    assert screen.buffer[0][0].data == '\u4e2d' and screen.buffer[0][2].data == '!'
    prefix = '\x1b[2J\x1b[H\u65e5\u672c\u8a9e'.encode('utf8')
    suffix = '\x1b[2J\x1b[Hsmall\x1b[20;3H>'.encode('utf8')
    raw = prefix + suffix
    boundaries = [{'offset': len(prefix), 'width': 60, 'height': 20}]
    screen = replay_capture(raw, boundaries)
    assert (screen.columns, screen.lines) == (60, 20)
    assert screen.display[0].startswith('small') and screen.display[19][2] == '>'
    proof = {'observed_offset': len(raw), 'resizes': boundaries,
             'frame': {'width': 60, 'height': 20, 'rows': screen.display,
                       'cursor_x': screen.cursor.x, 'cursor_y': screen.cursor.y,
                       'raw_bytes': len(raw), 'raw_sha256': hashlib.sha256(raw).hexdigest(),
                       'style_spans': style_spans(screen)}}
    assert verified_capture(raw, len(raw), proof).display == screen.display
    for boundary in ({'offset': -1, 'width': 60, 'height': 20},
                     {'offset': True, 'width': 60, 'height': 20},
                     {'offset': len(raw)+1, 'width': 60, 'height': 20},
                     {'offset': 0, 'width': 0, 'height': 20},
                     {'offset': 0, 'width': 60, 'height': 201},
                     {'offset': len(prefix)-1, 'width': 60, 'height': 20}):
        try:
            replay_capture(raw, [boundary])
        except ValueError:
            continue
        raise AssertionError('invalid offset, dimensions or split UTF-8 accepted')
    wrong_styles = [dict(span) for span in style_spans(screen)]
    wrong_styles[0]['reverse'] = True
    for key, value in (('width', 80), ('cursor_y', 0), ('cursor_x', True), ('raw_sha256', '0'*64), ('rows', ['wrong']), ('style_spans', wrong_styles)):
        changed = {**proof, 'frame': {**proof['frame'], key: value}}
        try:
            verified_capture(raw, len(raw), changed)
        except ValueError:
            continue
        raise AssertionError('mismatched verified frame accepted: '+key)
    print('terminal preview clear/cursor/resize/wide-cell, captured frame equality and invalid-boundary controls passed')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--deps', type=Path)
    parser.add_argument('--raw', type=Path)
    parser.add_argument('--observations', type=Path)
    parser.add_argument('--frame', type=Path, help='Saved .frame.json; require exact prefix hash, resize replay, rows and cursor.')
    parser.add_argument('--needle')
    parser.add_argument('--occurrence', type=int, default=1)
    parser.add_argument('--through-text', help='Extend the byte prefix through this exact captured line after the observation.')
    parser.add_argument('--output', type=Path)
    parser.add_argument('--theme', choices=('dark', 'light'), default='dark', help='QA terminal palette; does not claim the physical terminal theme.')
    parser.add_argument('--self-test', action='store_true')
    args = parser.parse_args()
    if args.deps:
        sys.path.insert(0, str(args.deps.resolve(strict=True)))
    if args.self_test:
        self_test()
        return 0
    if not all((args.raw, args.observations, args.needle, args.output)) or args.occurrence < 1:
        parser.error('Use --raw, --observations, --needle, --output and a positive occurrence.')
    if args.frame and args.through_text:
        parser.error('--frame uses the exact verified offset; it cannot be combined with --through-text.')
    observations = json.loads(args.observations.read_text(encoding='utf8'))['observations']
    matches = [item for item in observations if item['needle'] == args.needle]
    if len(matches) < args.occurrence:
        parser.error('No matching recorded observation.')
    observation = matches[args.occurrence-1]
    if 'observed_offset' not in observation:
        parser.error('Capture again with observed_offset; guessing a frame boundary is not evidence.')
    raw = args.raw.read_bytes()
    offset = observation['observed_offset']
    if type(offset) is not int or not 0 < offset <= len(raw):
        parser.error('Recorded observation offset is outside the raw capture.')
    if args.through_text:
        # A title can be observed before its frame finishes traversing the pipe.
        # Keep this explicit extension separate from measured observation time.
        needle = args.through_text.encode('utf8')
        found = raw.find(needle, offset)
        if found < 0:
            parser.error('The requested completion line does not follow this observation.')
        offset = found+len(needle)
    frame_proof = json.loads(args.frame.read_text(encoding='utf8')) if args.frame else None
    screen = verified_capture(raw, offset, frame_proof) if args.frame else replay_capture(raw[:offset])
    from PIL import Image, ImageDraw, ImageFont
    palette = {'default': '#dedede', 'black': '#1b1b1b', 'red': '#e08080',
               'green': '#9ebf8b', 'brown': '#d2b67e', 'blue': '#87a5cf',
               'magenta': '#b79acb', 'cyan': '#8bbbb8', 'white': '#dedede'}
    background = '#1b1b1b'
    if args.theme == 'light':
        background = '#ffffff'
        palette = {'default': '#202020', 'black': '#202020', 'red': '#b42318',
                   'green': '#1b6e32', 'brown': '#995700', 'blue': '#2456a6',
                   'magenta': '#8a3a82', 'cyan': '#006a78', 'white': '#eeeeee'}
    font_dir = Path('C:/Windows/Fonts')
    fonts = [ImageFont.truetype(str(font_dir/name), 18) for name in ('consola.ttf', 'malgun.ttf', 'seguiemj.ttf')]
    cell_w, cell_h, margin, title_h = 11, 25, 18, 42
    picture = Image.new('RGB', (screen.columns*cell_w+2*margin, screen.lines*cell_h+2*margin+title_h), background)
    draw = ImageDraw.Draw(picture)
    draw.text((margin, 12), f'Emulated captured ConPTY output | {screen.columns} x {screen.lines}', font=fonts[0], fill='#666666' if args.theme == 'light' else '#adadad')

    def colors(cell):
        fg = palette.get(cell.fg, '#'+cell.fg if len(cell.fg) == 6 else palette['default'])
        bg = background if cell.bg == 'default' else palette.get(cell.bg, '#'+cell.bg if len(cell.bg) == 6 else background)
        return (bg, fg) if cell.reverse else (fg, bg)

    # Paint backgrounds first so wide glyphs are not covered by continuation cells.
    for row in range(screen.lines):
        for column in range(screen.columns):
            _, bg = colors(screen.buffer[row][column])
            x, y = margin+column*cell_w, margin+title_h+row*cell_h
            draw.rectangle((x, y, x+cell_w-1, y+cell_h-1), fill=bg)
    for row in range(screen.lines):
        for column in range(screen.columns):
            cell = screen.buffer[row][column]
            if cell.data and cell.data != ' ':
                fg, _ = colors(cell)
                font = fonts[2] if any(ord(c) >= 0x1f000 for c in cell.data) else fonts[1] if any(0x2e80 <= ord(c) <= 0xffff for c in cell.data) else fonts[0]
                draw.text((margin+column*cell_w, margin+title_h+row*cell_h), cell.data, font=font, fill=fg)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    picture.save(args.output)
    args.output.with_suffix('.txt').write_text('\n'.join(screen.display)+'\n', encoding='utf8')
    receipt = {'scope': 'ANSI emulation of captured output bytes; not a physical screenshot or usability study',
               'raw_sha256': sha(args.raw), 'observation_sha256': sha(args.observations), 'observation': observation,
               'rendered_through_offset': offset, 'completion_text': args.through_text,
               'verified_frame_sha256': sha(args.frame) if args.frame else None,
               'verified_frame_equal': True if args.frame else None,
               'resize_boundaries': frame_proof.get('resizes', []) if args.frame else [],
               'tool_sha256': sha(Path(__file__)), 'png_sha256': sha(args.output),
               'dimensions': [screen.columns, screen.lines],
               'theme': args.theme,
               'dependencies': {name: importlib.metadata.version(name) for name in ('pyte', 'Pillow')},
               'limits': ['pyte and Pillow font shaping differ from Windows Terminal', 'default colors chosen for QA',
                          'cursor and exact grapheme glyph appearance are not certified']}
    args.output.with_suffix('.json').write_text(json.dumps(receipt, indent=2)+'\n', encoding='utf8')
    print(json.dumps({'output': str(args.output), 'dimensions': receipt['dimensions']}))
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
