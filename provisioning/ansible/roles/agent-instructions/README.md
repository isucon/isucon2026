# Participant coding-agent instructions

The `problem-server` image runs this role after extracting the webapp sources.
It installs `files/instructions.md` as `isucon:isucon`, mode `0644`, at these
paths relative to `/home/isucon`:

| Agent                      | Path                                                                                      |
| -------------------------- | ----------------------------------------------------------------------------------------- |
| Codex CLI                  | `.codex/AGENTS.md`                                                                        |
| Claude Code                | `.claude/CLAUDE.md`                                                                       |
| Gemini CLI                 | `.gemini/GEMINI.md`                                                                       |
| GitHub Copilot CLI         | `.copilot/copilot-instructions.md`                                                        |
| OpenCode                   | `.config/opencode/AGENTS.md`                                                              |
| Pi                         | `.pi/agent/AGENTS.md`                                                                     |
| Cursor CLI (project scope) | `AGENTS.md`, `webapp/AGENTS.md`, `webapp/{go,ruby,rust,python,php,perl,nodejs}/AGENTS.md` |

Cursor's documented file support is project-scoped, not a global user file. The
copies cover launches from the home directory, webapp root, and each supplied
implementation directory. They do not provide global coverage for arbitrary new
workspaces. Place an `AGENTS.md` in a new workspace when needed.

These paths assume default agent configuration directories. Overrides such as
`CODEX_HOME`, `COPILOT_HOME`, or `PI_CODING_AGENT_DIR` require copying the file
to the configured location. The role does not install agents or credentials and
does not configure root or the Router image.

Edit the common source file to update the baked instructions, then build with
`make ami ROLE=problem-server`. Rebuilding an AMI does not update existing
servers. Participants can edit their copies; rerunning this role overwrites
those edits.

## Validation

The instruction source adds a pacing paragraph with `sleep` to section 4 of the
supplied `fujiwara-agent.md`. An HTML comment marks the appended language and
daily-life notes. The regression test checks the pacing paragraph's location and
the original document's SHA-256 after removing that addition, without depending
on the contributor's Downloads directory. Do not reformat the imported portion.

With Ansible and its Python dependencies available in the active environment:

```sh
python -m unittest discover -s provisioning/ansible/tests -p 'test_agent_instructions.py'
```

The test runs the actual role twice against a temporary home with the current
user's ownership. It checks every destination, content, permissions, and
idempotence. It does not call agent APIs or verify model compliance.

## Official references

- [Codex](https://learn.chatgpt.com/docs/agent-configuration/agents-md)
- [Claude Code](https://code.claude.com/docs/en/memory)
- [Gemini CLI](https://geminicli.com/docs/cli/gemini-md/)
- [GitHub Copilot CLI](https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/add-custom-instructions)
- [OpenCode](https://opencode.ai/docs/rules/)
- [Pi](https://pi.dev/docs/latest/configuration)
- [Cursor CLI](https://cursor.com/docs/cli/using#rules)
