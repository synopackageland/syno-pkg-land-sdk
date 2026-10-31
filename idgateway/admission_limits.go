package idgateway

import "time"

// Bound one observed client, including clients behind a shared NAT. This does
// not trust browser IDs or forwarding headers as a way to obtain more slots.
const maxClientPending = 64

// addTxn never touches the authenticated session map or its mutex. Pruning
// runs at most once per second; repeated pre-auth requests do not scan maps.
func (f *Front) addTxn(id string, t txn) bool {
	f.txnMu.Lock()
	defer f.txnMu.Unlock()
	now := f.cfg.Now()
	if !now.Before(f.nextTxnSweep) {
		for k, v := range f.txns {
			if !now.Before(v.expires) {
				f.deleteTxn(k, v)
			}
		}
		f.nextTxnSweep = now.Add(time.Second)
	}
	if len(f.txns) >= maxPending || f.txnClients[t.ip] >= maxClientPending {
		return false
	}
	if f.txnClients == nil {
		f.txnClients = map[string]int{}
	}
	f.txns[id] = t
	f.txnClients[t.ip]++
	return true
}

// Caller holds txnMu.
func (f *Front) deleteTxn(id string, t txn) {
	delete(f.txns, id)
	if f.txnClients[t.ip] <= 1 {
		delete(f.txnClients, t.ip)
	} else {
		f.txnClients[t.ip]--
	}
}

// Authenticated bootstrap cannot evict another client's live sessions. A
// client must wait for expiry or revoke a session when its own cap is reached.
func (f *Front) addSession(id string, s session) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.cfg.Now()
	clients, users := 0, 0
	for k, v := range f.sessions {
		if !now.Before(v.expires) {
			delete(f.sessions, k)
			continue
		}
		if v.ip == s.ip {
			clients++
		}
		if v.subject.UID == s.subject.UID {
			users++
		}
	}
	if len(f.sessions) >= maxPending || clients >= maxClientPending || users >= maxClientPending {
		return false
	}
	f.sessions[id] = s
	return true
}
