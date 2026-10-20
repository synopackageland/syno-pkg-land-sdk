// Package filestation implements interactive NAS operations using the current
// DSM user's session. It never opens NAS data paths or retains credentials.
// Every typed operation uses a request-scoped session; DSM alone decides access.
package filestation
