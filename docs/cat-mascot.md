# Cat mascot runtime and design kit

UCLI's cat gives the terminal a small, recognizable companion without asking people to learn another interface. It follows the same priorities as the CLI: make actions discoverable, preserve drafts and context, and show what is actually happening. The rich terminal now embeds the reusable frame catalog. Lifecycle hooks select a sequence without downloading assets or changing task state; further placements remain design proposals.

The main cat has one face and two expressions. `(^.⩊ .^)/` is friendly and expressive for greetings and saved-result celebrations; Rest pairs the same mouth with closed `-` eyes. `(^._.^) ` is neutral, focused, or serious for work, search, preparation, and voice attention. A sequence keeps its mouth constant while its eyes or paw animate; a real state change chooses the next expression. The [animation catalog](../assets/mascot/cat-animations.json) defines eight sequences with eight-column source artwork. Playback reserves a nine-cell slot: it inserts one real ASCII space immediately after `⩊` and pads other expressions and ASCII frames to the same width. The [catalog checker](../tools/check_cat_animations.py) checks the source assets; Go playback tests check the rendered slot. A moving cat accompanies a status label; it never establishes that an operation started, succeeded, or stopped.

| Sequence | Current runtime trigger | Playback and settled state |
| --- | --- | --- |
| Hello (`wave`) | Interactive welcome or restored session | One bounded wave, then static |
| Working (`working`) | An actual `App.Run` call or voice transcription | Loop while the operation is active |
| Looking (`looking`) | Native sign-in checking and waiting | Loop until that operation ends; task search/routing is not connected in this slice |
| Paw tap (`paw`) | Voice preparation | Loop during preparation |
| Attentive (`listen`) | Active voice recording | Static cat alongside the real Recording label and stop control, until capture-stopped acknowledgement |
| Done (`done`) | `App.Run` returns a durably completed result without warnings | One celebration, then the happy static frame at `static_frame: 3` |
| Rest (`rest`) | Catalog available; no idle runtime hook in this slice | Closed `-` eyes and friendly mouth; bounded settling sequence, then static |
| Table peek (`peek`) | Offline workflow inspection | Static pose with an explicit no-task-started notice |

The runtime uses the single status row above the framed composer. The header cat stays static; at most one notice cat animates. Errors and warnings stop that animation and keep their full text priority. At constrained widths, the cat is omitted before shortening status or recovery information. Background rows do not animate independently. Animated empty-state and explicit help/about easter eggs remain proposals.

The standard 80-by-24 composition puts status on row 18, a rule on row 19, the editor on row 20, another rule on row 21, requested model and location on row 22, and key hints on row 23 (zero-based). Multiline composition expands upward; the final editor row and footer remain fixed. Standalone voice, sign-in and workflow panels show their own title and location without pretending to have a model selection or editable composer.

Real percentages, counts, reset timers, errors, permission scope, authentication text, and stop controls take priority over decoration. Animation does not establish that an operation started, succeeded, or stopped. It does not overwrite a draft, move keyboard focus, or delay an action until its last frame. Lifecycle hooks drive activity directly; submitting a menu command or saving a draft does not itself start the working sequence. Superseded ticks cannot restart an older operation. Voice preparation, recording and transcription remain distinct stages.

The friendly mouth is U+2A4A, UNION BESIDE AND JOINED WITH UNION; the focused mouth is ASCII `_`. These are two expressions of the same main cat. Table peek retains the separate tiny `ᨐ` and `ฅ` motif as an occasional Easter egg. Keep directional controls out of all frames. Unicode properties do not establish font coverage or actual terminal compatibility.

U+2A4A has neutral East Asian Width, rather than ambiguous width. Its terminal
cell advance and visible ink bounds are separate: a fallback font can draw past
the next character's origin. The added blank gives the mouth breathing room
without redefining Unicode width globally. It does not fix missing glyphs or
guarantee every terminal/font combination; use `ucli --ascii` when the cat still
looks wrong. The older Arabic `ﻌ` is not used as a fallback because it also has
Arabic directionality and compatibility-form behavior. See [Unicode width
guidance](https://www.unicode.org/reports/tr11/#Scope), [Unicode character
data](https://www.unicode.org/Public/UCD/latest/ucd/UnicodeData.txt), and
[Microsoft glyph metrics](https://learn.microsoft.com/en-us/windows/win32/api/dwrite/ns-dwrite-dwrite_glyph_metrics).

Use `--no-motion` for static poses while retaining rich navigation, and `--ascii` for ASCII cat artwork. `--no-color` and `NO_COLOR` affect color, not motion. There is no automatic operating-system reduced-motion detection or separate mascot-off flag in this slice. Plain and JSON output remain non-animated. ASCII mode maps the core mouth to `_`, Table peek's `ฅ` to a comma, and `ᨐ` to `^`; other interface symbols are unchanged. The mascot does not weaken `safe()` sanitization.

Catalog and runtime unit checks cover frame timing, static poses, stale ticks, cancellation, warning/error priority and explicit motion/ASCII controls. Packaged Windows checks reconstruct captured terminal frames and verify fixed positions, preserved composition and an ASCII static welcome. Follow the verification route for the current build's actual results; a test definition is not an executed pass. Physical font coverage, bidi behavior, accessibility and motion on macOS/Linux terminals remain unverified. Browser previews and ANSI emulation do not establish physical-terminal rendering or human usability.

Verify the catalog with `python tools/check_cat_animations.py --self-test` and follow [the project verification route](verification.md) when integrating it. The visual references are the [one-line animal archive](https://www.asciiart.eu/one-line/animals), [cat-paw collection](https://www.kaomojiya.org/en/cat-paw-kaomoji), and Unicode's [mathematical operators](https://www.unicode.org/charts/PDF/U2A00.pdf).
