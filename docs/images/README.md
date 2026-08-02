# Project images

Store public documentation images in this directory.

Before adding or replacing a dashboard screenshot:

- capture it from the current clean production build;
- use synthetic configuration, devices, status, and audit data;
- use only reserved documentation address ranges when an address must be shown;
- remove real public IP addresses, hostnames, MAC addresses, client names,
  usernames, tokens, keys, logs, and QR codes;
- avoid browser bookmarks, account avatars, local file paths, and desktop
  notifications;
- crop to the application window;
- prefer PNG when UI text requires lossless output;
- keep the source image readable at normal GitHub width;
- provide useful alt text in the document that embeds it.

The README image is:

```text
docs/images/dashboard-overview.png
```

It was captured automatically from the real React production build using a
synthetic API fixture. It is not a screenshot of a personal router, and it is not
a generated visual mockup.

Future replacements should preserve the same privacy and authenticity rules.
