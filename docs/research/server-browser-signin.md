# Running the sign-in browser on a server

Date: 2026-11-16. Host: Debian 13 cloud VM, Google Chrome 157, datacenter egress
address, no desktop. Goal: find a browser configuration that Google's sign-in
accepts so `gmcli auth` can run on a server and hand the window to a viewer.

## Method

A throwaway Go program started Chrome with the same flags `internal/browserauth`
uses (throwaway profile, Device Bound Session Credentials disabled, sync and
extensions off), opened the account chooser, and submitted a deliberately
non-existent address. No credentials were used. Google's reply distinguishes
"ran the account lookup" from "refused the browser".

| Configuration | `navigator.webdriver` | Result |
| --- | --- | --- |
| `--headless=new`, default UA | true | Lite sign-in page served; address field never became interactive |
| `--headless=new`, UA with `Headless` removed | true | "Couldn't sign you in. This browser or app may not be secure." |
| Visible window on Xvfb, chromedp default debugging port 0 | true | "This browser or app may not be secure." |
| Visible window on Xvfb, explicit loopback `--remote-debugging-port` (gmcli's setup) | false | "Couldn't find this account": lookup ran, browser accepted |
| Same, input delivered through DevTools `Input.*` from a viewer over a Unix socket | false | "Couldn't find this account" |

## Conclusions

- Google keys the "browser or app may not be secure" refusal on
  `navigator.webdriver`. Headless Chrome always reports it; a remote debugging
  port of 0 also puts Chrome into that mode. gmcli's existing choice of an
  explicit loopback port is what keeps the flag false.
- A datacenter egress address did not cause a refusal at the identifier step.
  Account-level challenges may still appear later and are the person's to
  answer.
- Input events dispatched over DevTools are trusted events; they did not
  trigger the refusal.
- Chrome ignores proxy credentials embedded in `HTTP_PROXY`; with such a proxy
  set the page fails with `ERR_INVALID_AUTH_CREDENTIALS`. Run without the
  variables on hosts that use an authenticated egress proxy.
- Chrome helper processes write cache and network state briefly after the main
  process exits; removing the profile needs a short retry loop.

## Design that followed

`gmcli auth --devtools-socket PATH`: use `DISPLAY` when set, otherwise start a
private Xvfb with `-displayfd` and `-nolisten tcp`; start Chrome as before;
relay its loopback DevTools port over a Unix socket (mode 0600); report the
socket and page target id in the `browser_ready` event. Viewers attach for
`Page.startScreencast` and `Input.*`. Everything after cookie capture is the
existing pairing path.
