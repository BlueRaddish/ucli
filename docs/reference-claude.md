# Interaction reference

UCLI's terminal interaction research draws on familiar native CLI conventions:
visible current choices, a separate keyboard focus, searchable selectors,
contextual hints and reversible navigation. The implementation is UCLI's own.
No proprietary implementation bundle or private native-program source audit is
distributed in this checkout.

Useful public references are Claude Code's
[interactive mode](https://code.claude.com/docs/en/interactive-mode),
[keybindings](https://code.claude.com/docs/en/keybindings) and
[model configuration](https://code.claude.com/docs/en/model-config). They describe
Claude Code, not guaranteed UCLI behavior. UCLI's actual keys and behavior belong
in its interface and [CLI guide](../internal/cli/README.md).

For each selector, keep committed settings separate from the highlighted preview.
Opening, searching or moving focus must not dispatch work. Confirmation changes
only the intended setting; cancellation restores the original composition and
selection. Empty results, unsupported choices and partial metadata need visible
states. Hints should describe the action currently available.

Check the same paths at narrow and wide sizes, with Unicode and multiline input,
while loading and after errors. Verify the saved draft, cursor, task identity,
revision and route as well as the visible frame. Keyboard-action counts and
screenshots do not establish comfort or human usability.

The [workflow interaction guide](workflow-interactions.md),
[experience specification](experience.md) and [verification route](verification.md)
record UCLI's contracts and repeatable checks.
