#!/usr/bin/env bash

set -euo pipefail

# ------------------------------------------------------------
# Continuum-WM - Git Checkpoint Helper
#
# Workflow:
#   1. Verify Git repository
#   2. Show branch and working-tree status
#   3. Show changed/untracked files
#   4. Show diff summary
#   5. Optional full diff review
#   6. Run project quality checks
#   7. Ask for commit message
#   8. Confirm before staging
#   9. Stage all reviewed changes
#  10. Review staged state
#  11. Optional staged diff review
#  12. Final confirmation
#  13. Commit
#  14. Verify final repository state
#
# This script NEVER pushes automatically.
# Branch creation, merges, rebases, resets, restores,
# and pushes remain explicit manual Git operations.
# ------------------------------------------------------------

bold() {
    printf '\033[1m%s\033[0m\n' "$1"
}

green() {
    printf '\033[32m%s\033[0m\n' "$1"
}

yellow() {
    printf '\033[33m%s\033[0m\n' "$1"
}

red() {
    printf '\033[31m%s\033[0m\n' "$1"
}

divider() {
    printf '\n%s\n' "------------------------------------------------------------"
}

# ------------------------------------------------------------
# 1. Verify repository
# ------------------------------------------------------------

if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    red "Error: This directory is not inside a Git repository."
    exit 1
fi

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

branch="$(git branch --show-current)"

divider
bold "Continuum-WM Git Checkpoint"
printf "Repository : %s\n" "$repo_root"
printf "Branch     : %s\n" "$branch"

# ------------------------------------------------------------
# 2. Check whether anything changed
# ------------------------------------------------------------

if [[ -z "$(git status --porcelain)" ]]; then
    divider
    green "Working tree is clean. Nothing to commit."
    exit 0
fi

# ------------------------------------------------------------
# 3. Show changed files
# ------------------------------------------------------------

divider
bold "Changed files"

git status --short

# ------------------------------------------------------------
# 4. Show diff summary
# ------------------------------------------------------------

divider
bold "Tracked-file diff summary"

if git diff --quiet; then
    printf "No unstaged tracked-file changes.\n"
else
    git diff --stat
fi

if ! git diff --cached --quiet; then
    divider
    bold "Already staged changes"
    git diff --cached --stat
fi

untracked="$(git ls-files --others --exclude-standard)"

if [[ -n "$untracked" ]]; then
    divider
    bold "Untracked files"
    printf '%s\n' "$untracked"
fi

# ------------------------------------------------------------
# 5. Optional detailed review
# ------------------------------------------------------------

divider
read -r -p "Review the full tracked-file diff? [y/N] " review_diff

if [[ "$review_diff" =~ ^[Yy]$ ]]; then
    if command -v less >/dev/null 2>&1; then
        git diff --color=always | less -R
    else
        git diff
    fi
fi

# ------------------------------------------------------------
# 6. Run Continuum-WM quality checks
# ------------------------------------------------------------

divider
bold "Running Continuum-WM quality checks"

printf '\n> cargo fmt --check\n'
cargo fmt --check

printf '\n> cargo clippy -- -D warnings\n'
cargo clippy -- -D warnings

printf '\n> cargo test\n'
cargo test

green "All quality checks passed."

# ------------------------------------------------------------
# 7. Ask for commit message
# ------------------------------------------------------------

divider
bold "Commit message"

commit_message=""

while [[ -z "$commit_message" ]]; do
    read -r -p "Enter commit message: " commit_message

    commit_message="$(
        printf '%s' "$commit_message" \
        | sed 's/^[[:space:]]*//;s/[[:space:]]*$//'
    )"

    if [[ -z "$commit_message" ]]; then
        yellow "Commit message cannot be empty."
    fi
done

# ------------------------------------------------------------
# 8. Confirmation before staging
# ------------------------------------------------------------

divider
printf "Branch  : %s\n" "$branch"
printf "Commit  : %s\n" "$commit_message"

printf '\nFiles that will be staged:\n'
git status --short

printf '\n'
read -r -p "Stage ALL listed changes? [y/N] " confirm_stage

if [[ ! "$confirm_stage" =~ ^[Yy]$ ]]; then
    yellow "Checkpoint cancelled. No additional files were staged."
    exit 0
fi

# ------------------------------------------------------------
# 9. Stage everything
# ------------------------------------------------------------

git add -A

# ------------------------------------------------------------
# 10. Verify staged state
# ------------------------------------------------------------

divider
bold "Staged changes"

git status --short

divider
bold "Staged diff summary"

git diff --cached --stat

if git diff --cached --quiet; then
    red "Nothing is staged. Commit aborted."
    exit 1
fi

# ------------------------------------------------------------
# 11. Optional staged diff review
# ------------------------------------------------------------

divider
read -r -p "Review the exact staged diff before commit? [y/N] " review_staged

if [[ "$review_staged" =~ ^[Yy]$ ]]; then
    if command -v less >/dev/null 2>&1; then
        git diff --cached --color=always | less -R
    else
        git diff --cached
    fi
fi

# ------------------------------------------------------------
# 12. Final confirmation
# ------------------------------------------------------------

divider
printf "Branch : %s\n" "$branch"
printf "Commit : %s\n" "$commit_message"

read -r -p "Create this commit? [y/N] " confirm_commit

if [[ ! "$confirm_commit" =~ ^[Yy]$ ]]; then
    yellow "Commit cancelled."
    yellow "Note: reviewed changes remain staged."
    exit 0
fi

# ------------------------------------------------------------
# 13. Commit
# ------------------------------------------------------------

divider
bold "Creating commit..."

git commit -m "$commit_message"

# ------------------------------------------------------------
# 14. Final verification
# ------------------------------------------------------------

divider
bold "Verification"

git status

divider
bold "Latest commit"

git log --oneline --decorate -1

divider

if [[ -z "$(git status --porcelain)" ]]; then
    green "Checkpoint complete. Working tree is clean."
else
    yellow "Commit succeeded, but the working tree still contains changes."
    git status --short
fi

printf '\n'
yellow "Nothing was pushed."
yellow "Push explicitly when you are ready: git push origin \"$branch\""
