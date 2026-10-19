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


def self_test():
    screen, stream = emulator()
    stream.feed('\x1b[2J\x1b[Habc\x1b[2;1HX')
    assert screen.display[0].startswith('abc') and screen.display[1].startswith('X')
    stream.feed('\x1b[8;20;60t')
    assert (screen.columns, screen.lines) == (60, 20)
    stream.feed('\x1b[H\x1b[2K\u4e2d!')
    assert screen.buffer[0][0].data == '\u4e2d' and screen.buffer[0][2].data == '!'
    print('terminal preview clear/cursor/resize/wide-cell self-check passed')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--deps', type=Path)
    parser.add_argument('--raw', type=Path)
    parser.add_argument('--observations', type=Path)
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
    observations = json.loads(args.observations.read_text(encoding='utf8'))['observations']
    matches = [item for item in observations if item['needle'] == args.needle]
    if len(matches) < args.occurrence:
        parser.error('No matching recorded observation.')
    observation = matches[args.occurrence-1]
    if 'observed_offset' not in observation:
        parser.error('Capture again with observed_offset; guessing a frame boundary is not evidence.')
    raw = args.raw.read_bytes()
    offset = observation['observed_offset']
    if not 0 < offset <= len(raw):
        parser.error('Recorded observation offset is outside the raw capture.')
    if args.through_text:
        # A title can be observed before its frame finishes traversing the pipe.
        # Keep this explicit extension separate from measured observation time.
        needle = args.through_text.encode('utf8')
        found = raw.find(needle, offset)
        if found < 0:
            parser.error('The requested completion line does not follow this observation.')
        offset = found+len(needle)
    screen, stream = emulator()
    stream.feed(raw[:offset].decode('utf8', errors='strict'))
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
