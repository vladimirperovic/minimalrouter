# Maintainer release process

This document describes the safe publication process for Minimal Router OS.
It is intentionally conservative because a repository can contain sensitive
information outside the visible `main` tree, including old commits, pull request
refs, issue discussions, workflow logs, and artifacts.

## Core rule

Never make an original private development repository public when it contains
history or metadata that was not reviewed for disclosure.

Rewriting `main` alone does not guarantee that old pull request refs, issues,
comments, workflow logs, or artifacts disappear. The safe release uses a
brand-new repository with one reviewed root commit while the original repository
remains a private archive.

## Before the release session

The following must already be true:

- the complete private history is mirrored and independently verified;
- the checked-out source ref contains the reviewed public tree;
- standard CI and the current-tree secret scan pass;
- CodeQL analysis completes while the repository is private;
- runtime state, credentials, snapshots, backups, packet captures, local
  environment files, and internal handoff notes are absent;
- every screenshot and example uses synthetic data;
- any credential that appeared in private history has been rotated.

## Create the one-commit candidate locally

Check out the exact reviewed commit, verify that the working tree is clean, and
run:

```sh
git status --short
git rev-parse HEAD
sh scripts/prepare-public-root.sh HEAD /tmp/minimalrouter-public-root
```

An explicit reviewed branch, tag, or commit SHA may be supplied instead of
`HEAD`.

The script:

- exports only the selected source tree;
- removes the private staging checklist when present;
- rejects known internal/runtime paths and suspicious secret-bearing files;
- initializes a new repository with exactly one commit;
- verifies that it has no tags or remotes;
- runs a full-history Gitleaks scan;
- never pushes, renames, changes visibility, or publishes anything.

Review the result:

```sh
git -C /tmp/minimalrouter-public-root log --oneline --decorate --all
git -C /tmp/minimalrouter-public-root ls-files
git -C /tmp/minimalrouter-public-root status --short
git -C /tmp/minimalrouter-public-root remote -v
```

Expected state: one commit, a clean working tree, and no remote.

## Owner-reviewed GitHub cutover

Perform these steps together with the repository owner. Keep every repository
private until the final visibility step.

1. Reconfirm the private mirrors and record their current commit IDs.
2. Rename the original private repository to a clearly private archive name.
3. Confirm that the renamed archive is still private and still contains the old
   pull requests, issues, workflow history, branches, and tags.
4. Create a new **private**, empty repository with the intended public name. Do
   not add a README, license, or `.gitignore` from GitHub's creation screen.
5. Add that new private repository as the only remote of the clean candidate and
   push its single `main` commit.
6. Confirm on GitHub that the new repository has one branch, one commit, no tags,
   no old pull requests/issues, and no unexpected Actions artifacts.
7. Wait for CI and both current-tree and full-history Gitleaks jobs to pass.
8. Confirm that CodeQL completes in private analysis-only mode. SARIF upload is
   intentionally disabled while a personal repository is private.
9. Configure the repository description, topics, Actions permissions, branch
   protection/rulesets, issue settings, Discussions decision, and release
   permissions.
10. Review the README, screenshot, license, security policy, comparison text,
    and installation warnings directly on GitHub.
11. Change visibility to public only after the owner gives an explicit final
    approval.
12. Re-run CodeQL after publication so its SARIF results upload to GitHub Code
    Scanning, then confirm the Security tab and all status badges are healthy.

Reusing the old repository name disables GitHub's automatic redirect from the
renamed archive. That is intentional here: the public URL must resolve to the
new clean repository, while the historical repository remains private.

GitHub reference:

- https://docs.github.com/en/repositories/creating-and-managing-repositories/renaming-a-repository
- https://docs.github.com/en/repositories/managing-your-repositorys-settings-and-features/managing-repository-settings/setting-repository-visibility

## Local remote cleanup after cutover

Existing local clones may still point at the old URL. Do not assume any remote is
correct after a rename/recreation sequence.

For each private checkout, explicitly set `origin` to its intended private
repository URL, which should be stored outside this public source tree:

```sh
git remote set-url origin <PRIVATE_REPOSITORY_URL>
git remote -v
```

For a fresh public checkout, use the URL shown on the new public repository's
GitHub page.

Keep private and public development in separate local directories to reduce the
chance of pushing a household-specific change to the public project.

## Rollback

Until visibility is changed, rollback is simple:

- leave the renamed archive private and untouched;
- keep the new clean repository private;
- delete and recreate only the new clean repository if its metadata or history
  is not exactly as expected;
- rebuild the one-commit candidate from the reviewed cleanup branch;
- rerun all checks before resuming.

After publication, do not rewrite a released public history casually. Revoke and
rotate any exposed credential immediately, document the incident, and follow the
security response process in `SECURITY.md`.
