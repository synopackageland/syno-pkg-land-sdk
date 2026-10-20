// Package idgateway signs DSM users in to a packaged web app without a
// separate sign-in. It has two halves:
//
//   - The authorize CGI runs on the DSM origin as the package account. Only
//     there does the browser hold both the DSM session cookie and a SynoToken
//     DSM accepts, so only there can DSM's authenticate.cgi verify the
//     session with cookie, token and client address. The CGI then checks
//     the account in NSS and hands the gateway a short-lived signed
//     Assertion.
//   - The gateway is a long-running, non-root daemon on the app's ports. It
//     proxies to the app on loopback and delivers the verified Subject in the
//     app's declared mode: an embedded OpenID Connect provider ("oidc"),
//     trusted headers ("header") or an adapter-supplied app session
//     ("token").
//
// Users are keyed by DSM uid, never by name.
package idgateway
