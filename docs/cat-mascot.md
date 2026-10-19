# Cat mascot design kit

UCLI's cat gives the terminal a small, recognizable companion without asking people to learn another interface. It follows the same priorities as the CLI: make actions discoverable, preserve drafts and context, and show what is actually happening. This is an animation design kit and runtime proposal. The reusable catalog specifies frame order and timing; application integration is proposed below.

The main cat has one face and two expressions. `(^.⩊.^)/` is friendly and expressive for greetings and saved-result celebrations. `(^._.^) ` is neutral, focused, or serious for work, search, preparation, voice attention, and rest. A sequence keeps its mouth constant while its eyes or paw animate; a real state change chooses the next expression. The [animation catalog](../assets/mascot/cat-animations.json) defines eight sequences, each with an eight-column layout budget, including padding. The [catalog checker](../tools/check_cat_animations.py) checks the assets. A moving cat accompanies a status label; it never establishes that an operation started, succeeded, or stopped.

| Sequence | Valid trigger and placement | Playback and settled state |
| --- | --- | --- |
| Hello (`wave`) | Welcome or resume, in the notice slot | One bounded wave, then static |
| Working (`working`) | Actual agent activity | Loop only while that operation is busy |
| Looking (`looking`) | Current search or routing operation | Loop until that generation finishes or is canceled |
| Paw tap (`paw`) | Real handoff, preparation, or task activity | Loop while the activity exists; never imply unimplemented execution |
| Attentive (`listen`) | Voice preparation | Loop during preparation; actual Recording uses a static attentive cat with its real status and stop control |
| Done (`done`) | A completed result has been durably persisted | One celebration, then the calm happy frame `(^~⩊~^) ` at `static_frame: 3` |
| Rest (`rest`) | Idle or empty views | Settle once into rest; a paused interface stays static |
| Table peek (`peek`) | Optional empty-view or explicit help/about easter egg | One bounded peek above a table, then its settled pose |

The first proposed home is the single notice row immediately above the fixed composer. Keep the header static and allow at most one actively animated cat across the interface. A focused task or workflow may own that indicator; background rows should not each animate. Empty history and workflow views normally use calm static rest. A longer easter egg belongs behind an explicit help/about action, with a bounded ending.

Real percentages, counts, reset timers, errors, permission scope, authentication text, and stop controls take priority over decoration. Animation must not overwrite a draft, move keyboard focus, shift the fixed layout, or delay an action until its last frame. Pausing motion leaves a static pose.

Bind motion to operation lifecycles, not screen text. In particular, `composer_terminal.go` currently paints “Working” after any Enter, including draft and menu actions; that label cannot drive a busy animation. Search indicators must follow the current generation. Voice preparation, recording, capture stopped, and transcription remain distinct stages, with explicit labels throughout.

The friendly mouth is U+2A4A, UNION BESIDE AND JOINED WITH UNION; the focused mouth is ASCII `_`. These are two expressions of the same main cat. Table peek retains the separate tiny `ᨐ` and `ฅ` motif as an occasional Easter egg. Keep directional controls out of all frames. Unicode properties do not establish font coverage or actual terminal compatibility.

Provide off, static, reduced-motion, and ASCII fallbacks. The core seven replace the mouth with `_`; Table peek maps `ฅ` to a comma and `ᨐ` to `^`. Plain and JSON output stay stable and non-animated. Do not weaken `safe()` sanitization for the mascot. Next, verify lifecycle transitions and cancellation alongside frame durations, then test fonts, bidi behavior, cell widths, and fixed coordinates on actual Windows, macOS, and Linux terminals. A browser preview demonstrates composition, not physical-terminal behavior or human usability.

Verify the catalog with `python tools/check_cat_animations.py --self-test` and follow [the project verification route](verification.md) when integrating it. The visual references are the [one-line animal archive](https://www.asciiart.eu/one-line/animals), [cat-paw collection](https://www.kaomojiya.org/en/cat-paw-kaomoji), and Unicode's [mathematical operators](https://www.unicode.org/charts/PDF/U2A00.pdf).
