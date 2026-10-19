"""Validate the offline cat animation catalog; does not render a terminal."""
import argparse
from copy import deepcopy
import json
from pathlib import Path
import sys

ROOT = Path(__file__).resolve().parents[1]
IDS = {'wave', 'working', 'looking', 'paw', 'listen', 'done', 'rest', 'peek'}
MOUTH = '\ufecc'
MAX_BYTES = 128 * 1024


def require(condition, message):
    if not condition:
        raise ValueError(message)


def keys(value, expected, where):
    require(type(value) is dict and set(value) == set(expected), where + ': fields differ from schema')


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        require(key not in result, 'duplicate JSON field: ' + key)
        result[key] = value
    return result


def decode(raw):
    require(len(raw) <= MAX_BYTES, 'catalog exceeds 128 KiB')
    return json.loads(raw.decode('utf-8'), object_pairs_hook=unique_object,
                      parse_constant=lambda _: require(False, 'non-finite JSON number'))


def validate(data):
    keys(data, ('schema_version', 'columns', 'mouth', 'ascii_mouth', 'animations'), 'catalog')
    require(type(data['schema_version']) is int and data['schema_version'] == 1, 'unsupported schema_version')
    require(type(data['columns']) is int and data['columns'] == 8, 'columns must be 8')
    require(data['mouth'] == MOUTH and data['ascii_mouth'] == '_', 'mouth/fallback differ from the selected pair')
    animations = data['animations']
    require(type(animations) is list and len(animations) == len(IDS), 'expected eight animations')
    seen, playback, frame_count = set(), set(), 0
    for animation in animations:
        keys(animation, ('id', 'name', 'playback', 'static_frame', 'placement', 'frames'), 'animation')
        identity = animation['id']
        require(type(identity) is str and identity in IDS and identity not in seen, 'unknown or duplicate animation ID')
        seen.add(identity)
        for field in ('name', 'placement'):
            label = animation[field]
            require(type(label) is str and bool(label.strip()) and len(label) <= 160,
                    identity + ': ' + field + ' must be nonempty bounded text')
        mode = animation['playback']
        require(type(mode) is str and mode in {'loop', 'once'}, identity + ': unknown playback')
        if identity == 'peek':
            require(mode == 'once', 'peek: table reveal must play once')
        playback.add(mode)
        frames = animation['frames']
        # Small finite frame sets keep this design asset reviewable. A broader
        # animation format would need its own version, not unbounded input here.
        require(type(frames) is list and 2 <= len(frames) <= 64, identity + ': expected 2..64 frames')
        static = animation['static_frame']
        require(type(static) is int and 0 <= static < len(frames), identity + ': invalid static_frame')
        variants, duration = set(), 0
        peek_hidden, peek_visible = False, 0
        for index, frame in enumerate(frames):
            where = identity + ': frame ' + str(index)
            keys(frame, ('text', 'ms'), where)
            text, ms = frame['text'], frame['ms']
            require(type(text) is str and len(text) == data['columns'], where + ': expected eight codepoints')
            if identity == 'peek':
                # This is the one selected table-reveal primitive, not a general
                # extra-glyph permission for the seven mouth-based animations.
                require(all(char in '\u0e05\u1a10' or 32 <= ord(char) <= 126 for char in text),
                        where + ': only U+0E05, U+1A10 and printable ASCII are permitted')
                hidden = text.count('_') == 5 and set(text) <= {'_', ' '}
                if hidden:
                    require(index == 0, where + ': only the initial frame may hide the face')
                    peek_hidden = True
                else:
                    require(text.count('\u1a10') == 1, where + ': expected exactly one U+1A10 face')
                    peek_visible += 1
                fallback = text.replace('\u0e05', ',').replace('\u1a10', '^')
            else:
                require(text.count(MOUTH) == 1, where + ': expected exactly one U+FECC mouth')
                require(all(char == MOUTH or 32 <= ord(char) <= 126 for char in text),
                        where + ': only the mouth and printable ASCII are permitted')
                fallback = text.replace(MOUTH, data['ascii_mouth'])
                candidate = text.replace(MOUTH, '\u2a4a')
                require(len(candidate) == 8 and candidate.count('\u2a4a') == 1
                        and all(char == '\u2a4a' or 32 <= ord(char) <= 126 for char in candidate),
                        where + ': invalid U+2A4A preview candidate')
            # This restricted alphabet has expected width 1 per character in
            # UCLI's pinned width table. Font/bidi/physical terminal proof is separate.
            require(len(fallback) == 8 and all(32 <= ord(char) <= 126 for char in fallback),
                    where + ': invalid ASCII fallback')
            require(type(ms) is int and 120 <= ms <= 2000, where + ': timing must be 120..2000 ms')
            variants.add(text)
            duration += ms
        require(len(variants) > 1, identity + ': animation needs distinct frames')
        require(duration > 0, identity + ': duration must be positive')
        if identity == 'peek':
            require(peek_hidden and peek_visible > 0, 'peek: needs an initial hidden frame and a visible face')
        frame_count += len(frames)
    require(playback == {'loop', 'once'}, 'both loop and once playback must be represented')
    return frame_count


def self_test(data):
    validate(data)
    core = next(index for index, animation in enumerate(data['animations']) if animation['id'] != 'peek')
    peek = next(index for index, animation in enumerate(data['animations']) if animation['id'] == 'peek')
    baseline = data['animations'][core]['frames'][0]['text']
    ordinary = next(index for index, char in enumerate(baseline) if char != MOUTH)
    def substitute(char):
        return baseline[:ordinary] + char + baseline[ordinary + 1:]
    cases = [
        (('columns',), True),
        (('animations', core, 'id'), data['animations'][(core + 1) % len(IDS)]['id']),
        (('animations', core, 'playback'), 'repeat'),
        (('animations', core, 'static_frame'), True),
        (('animations', core, 'static_frame'), len(data['animations'][core]['frames'])),
        (('animations', core, 'frames', 0, 'text'), baseline[:-1]),
        (('animations', core, 'frames', 0, 'text'), baseline.replace(MOUTH, '_')),
        (('animations', core, 'frames', 0, 'text'), substitute(MOUTH)),
        (('animations', core, 'frames', 0, 'text'), substitute('\u202e')),
        (('animations', core, 'frames', 0, 'text'), substitute('\u200b')),
        (('animations', core, 'frames', 0, 'text'), substitute('\n')),
        (('animations', core, 'frames', 0, 'text'), substitute('\u0e05')),
        (('animations', core, 'frames', 0, 'ms'), True),
        (('animations', core, 'frames', 0, 'ms'), 119),
        (('animations', core, 'frames', 0, 'ms'), 2001),
        (('animations', peek, 'playback'), 'loop'),
        (('animations', peek, 'frames', 0, 'text'), '__\u1a10__   '),
        (('animations', peek, 'frames', 1, 'text'), '_____   '),
        (('animations', peek, 'frames', 1, 'text'), '_\u0e05_\u0e05_   '),
        (('animations', peek, 'frames', 1, 'text'), '_\u1a10\u1a10__   '),
        (('animations', peek, 'frames', 1, 'text'), '_\u0e05\ufecc\u0e05_   '),
        (('animations', peek, 'frames', 1, 'text'), '_\u0e31\u1a10\u0e05_   '),
    ]
    broken = []
    for path, value in cases:
        copy = deepcopy(data)
        target = copy
        for part in path[:-1]:
            target = target[part]
        target[path[-1]] = value
        broken.append(copy)
    for mode in ('once', 'loop'):
        copy = deepcopy(data)
        for animation in copy['animations']:
            animation['playback'] = mode
        broken.append(copy)
    copy = deepcopy(data)
    for frame in copy['animations'][core]['frames']:
        frame['text'] = baseline
    broken.append(copy)
    for copy in broken:
        try:
            validate(copy)
        except ValueError:
            continue
        raise AssertionError('malformed animation fixture was accepted')
    for raw in (b'{"schema_version":1,"schema_version":1}', b'{"n":NaN}', b'{'):
        try:
            decode(raw)
        except ValueError:
            continue
        raise AssertionError('malformed JSON was accepted')
    print(f'PASS: valid catalog plus {len(broken)} malformed-frame/schema and 3 JSON negative controls')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('catalog', nargs='?', type=Path, default=ROOT / 'assets/mascot/cat-animations.json')
    parser.add_argument('--self-test', action='store_true')
    args = parser.parse_args()
    try:
        with args.catalog.open('rb') as source:
            data = decode(source.read(MAX_BYTES + 1))
        frames = validate(data)
        if args.self_test:
            self_test(data)
    except (OSError, ValueError) as error:
        print('FAIL: ' + str(error), file=sys.stderr)
        return 1
    print(f'PASS: {len(IDS)} animations, {frames} frames, eight expected cells, ASCII fallbacks and U+2A4A candidates')
    print('Scope: offline catalog only; font coverage, bidi and actual terminal rendering remain unverified')
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
