# Contributing to ChipIn

Thanks for helping out. See [`PLAN.md`](./PLAN.md) for the design and what's deliberately out of
scope, and [`AGENTS.md`](./AGENTS.md) for the layout and the decisions behind it.

## Setup

```sh
mise install                    # pinned toolchain (Go, golangci-lint, GoReleaser, Java)
source ./firestore.sh start     # optional: runs the Firestore emulator (needs Java; downloads it once; stops itself after 3h)
go test -race ./... && golangci-lint run
```

Tests never touch live Slack or Linear. The Firestore tests run against the emulator and skip
themselves when `FIRESTORE_EMULATOR_HOST` is unset; CI always runs them. To try the bot against a
real Slack workspace, see the README (Socket Mode needs no public URL).

## Pull requests

- Keep a change focused, and add or update tests with it. Anything that touches storage should be
  covered in the shared contract test in `internal/store/store_test.go`, which runs against both
  the local JSON file store and Firestore.
- Use [Conventional Commits](https://www.conventionalcommits.org) for commit messages (`feat:`,
  `fix:`, `docs:`, `ci:`, `chore:` ...), because releases are generated from them: `fix:` makes a
  patch release, `feat:` a minor one, and `!` or a `BREAKING CHANGE:` footer a major one. `docs:`,
  `ci:` and `chore:` don't cut a release on their own.
- Don't hand-edit the version numbers in the README's install and verification examples: release
  PRs update them (the marked blocks, see `AGENTS.md`).
- Releases are GitHub *immutable* releases, so a published version can never be edited. The
  pipeline is described in the README and `PLAN.md` §7.

## AI-assisted commits (optional)

If you use a coding agent, we'd like commits it makes to say so: add an `Assisted-by:` trailer
naming the tool and the model, for example

```
Assisted-by: OpenCode v2.1.1 (github-copilot/claude-sonnet-5.5)
```

You can type that by hand, but a git alias makes it automatic, and lets you give agents their own
signing key. That's useful when your normal signing key needs you present (a hardware key, or an
SSH agent that asks for approval on every use), since a commit the agent makes would otherwise stop
and wait for you.

1. Create a key just for agents, and add its public half to GitHub as a **signing key**
   (Settings → SSH and GPG keys → New SSH key → Key type: Signing Key). It has no passphrase, so
   keep it `0600` and out of any repo; GitHub signing keys apply to your whole account, so delete the
   key there to revoke it.

   ```sh
   ssh-keygen -t ed25519 -N "" -C "agent signing key" -f ~/.ssh/agent-signing-ed25519
   ```

2. Add a global alias. This one signs with that key and adds the trailer, taking the tool version
   from `opencode --version` and the model from the `AGENT_MODEL` environment variable. Adapt the
   tool name and version lookup for your agent.

   ```sh
   git config --global alias.agent-commit '!f() { v="${AGENT_VERSION:-$(opencode --version 2>/dev/null | head -1)}"; v="${v#opencode }"; git -c gpg.format=ssh -c gpg.ssh.program=ssh-keygen -c user.signingkey="$HOME/.ssh/agent-signing-ed25519" -c commit.gpgsign=true commit --trailer "Assisted-by: OpenCode ${v:-unknown version} (${AGENT_MODEL:-unknown model})" "$@"; }; f'
   ```

3. Have the agent commit with it, naming the model it's running as:

   ```sh
   AGENT_MODEL="github-copilot/claude-sonnet-5.5" git agent-commit -m "fix: something"
   ```

   It takes the same arguments as `git commit`. Your own `git commit` is unaffected.

For pushing without an interactive prompt, a second remote over HTTPS that authenticates through
the GitHub CLI works well (`git remote add gh https://github.com/<owner>/<repo>.git`, plus
`gh auth setup-git`, or a repo-local `credential.helper` of `!gh auth git-credential`).
