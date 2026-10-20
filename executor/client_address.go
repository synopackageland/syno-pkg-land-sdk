package main

import "context"

// localAddress is the NAS's own address. Work no browser asked for (root's
// delegate.setScope, a grant that recorded no browser) is checked as coming
// from the NAS itself, so a rule limited to other addresses refuses it.
const localAddress = "127.0.0.1"

type clientAddressKey struct{}

func withClientAddress(ctx context.Context, addr string) context.Context {
	return context.WithValue(ctx, clientAddressKey{}, addr)
}

// withRequestAddress gives a request the caller's address. A request with
// caller authentication has only the address it sent, possibly none.
func withRequestAddress(ctx context.Context, auth Auth) context.Context {
	if auth.present() {
		return withClientAddress(ctx, auth.RemoteAddr)
	}
	return withClientAddress(ctx, localAddress)
}

// withGrantAddress checks work a grant does without a session from the
// browser address that grant or operation recorded.
func withGrantAddress(ctx context.Context, auth Auth, recorded string) context.Context {
	if auth.present() {
		return ctx
	}
	if recorded == "" {
		recorded = localAddress
	}
	return withClientAddress(ctx, recorded)
}

func clientAddress(ctx context.Context) (string, bool) {
	addr, ok := ctx.Value(clientAddressKey{}).(string)
	return addr, ok
}
