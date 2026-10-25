# Accounts

One account signs in to the dashboard and to `docker login`. How the
client sends the password is in [Registry](REGISTRY.md).

## Bootstrap

`REGISTRY_ADMIN_USER` and `REGISTRY_ADMIN_PASSWORD` create the first
administrator when both are set and that username does not exist yet.
Set both, or neither. The process exits when only one is set. A later
start does not rewrite an existing user.

That account is the only administrator. The dashboard cannot create
another one.

## People and service accounts

**Accounts** is visible to the administrator.

- A person has a password of at least 8 characters and can open the
  dashboard.
- A service account has no dashboard password. Creating it returns a
  token once. **New token** replaces that token, and the previous one
  stops immediately. The plaintext starts with `wharf_` and is not
  stored. `docker login` uses it as the password.

You cannot delete the account you are signed in as. You cannot delete
the last administrator. A taken username is `409`.

A username starts with a letter or digit. The rest is letters, digits,
`.`, `_`, or `-`, up to 63 characters.

## Personal access tokens

**Tokens** is visible to every signed-in person. A service account
cannot open it.

Create a token, name it, and copy it when it is shown. The plaintext
is not stored. Revoke stops that token. The password, and any other
tokens for that person, keep working.

## What a signed-in account can do

Every signed-in account can see, push, delete, and collect the whole
registry. The user label is not a permission. Account management stays
admin-only. There are no per-repository roles.
