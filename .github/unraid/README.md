# Unraid Community Applications template

`bindery.xml` is the template Unraid's Community Applications plugin reads to
render the install page for Bindery. Two feeds carry it:

- **Community-maintained (fast track):** mirrored at
  [`selfhosters/unRAID-CA-templates`](https://github.com/selfhosters/unRAID-CA-templates).
  Lands within a day of a PR merging there.
- **First-party (this file):** registered with
  [`Squidly271/AppFeed`](https://github.com/Squidly271/AppFeed). The plugin
  fetches this raw URL directly and shows Bindery as "by vavallee".

## Updating on release

The `<Repository>` tag is `:latest`, so most releases need no template
change — Unraid users get the new image by clicking "Force Update". Edit
the template only when:

- The container surface changes — new env var, new port, new required
  volume, removed env var. Mirror the change here and in
  `selfhosters/unRAID-CA-templates`.
- A default needs to change — e.g. switching the default `BINDERY_*`
  flag from `false` to `true`.
- Major version bumps where the template's description should mention
  new headline features.

When you do edit, also bump the corresponding entry in the selfhosters
fork. The two files should stay in lock-step apart from the
`<TemplateURL>` value, which differs per feed.

## Validating

```bash
xmllint --noout .github/unraid/bindery.xml
curl -sI "https://raw.githubusercontent.com/vavallee/bindery/main/.github/assets/logo.png" | head -1
```

If you have an Unraid box (or a VM): Apps → "Click here to add a missing
application" → paste the raw URL of `bindery.xml`. The form should render
1 port, 4 paths, 3 always-visible env vars, and a handful of advanced
ones. Click Apply, watch the container come up, hit
`http://<unraid-ip>:8787/`.
