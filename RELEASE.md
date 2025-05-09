# Kodelet

## 0.0.4.alpha (2025-05-09)

- Added new `watch` command to monitor file changes and provide AI assistance, support for special `@kodelet` comments to trigger automatic code analysis and generation.
- Improved chat TUI with better text wrapping and no character limit
- Added `--short` flag to commit command for generating concise commit messages

## 0.0.3.alpha1 (2025-05-09)

- Reduce the log level of README.md and KODELET.md to `debug` to avoid cluttering the console output.
## 0.0.3.alpha (2025-05-09)

- Minor tweaks on the chat TUI (e.g. a rad ascii art and processing spinner)
- Added a new command `/help` to show the help message
- Added a new command `/clear` to clear the screen
- Added a new command `/bash` to execute the chat context

### Bug fixes

- Stream out the output from the llm whenever the it responds, instead of buffering it.
- Use `YYYY-MM-DD` in the system prompt instead of the time, so that we can have more efficient cache control for the purpose of cost optimisation.

## 0.0.2.alpha1

Initial release of the kodelet
