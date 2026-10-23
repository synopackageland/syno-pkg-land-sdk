// Package filestation retains typed file contracts and custom authorized adapters.
// The former browser-session HTTP relay is retired: Client operations return
// unsupported without I/O. Interactive file operations use the browser SDK's
// same-origin DSM transport. Backend work needs an independent authorization
// mechanism; never substitute a package or administrator account for a caller.
package filestation
