# App-owned notification delivery

The Go `notify` package runs as the package account, uses the App's own HTTPS
webhook or SMTP settings, and never reads DSM's SMTP credentials. It is a
library: the App keeps its worker, event log, destination revisions and outbox.
No daemon, root permission or machine-wide notification policy is required.

## Admit an event

```go
n := notify.Notifier{
    Events: map[string]notify.Event{
        "device.offline": {Vars: []string{"device"}, Throttle: 10 * time.Minute},
    },
    Recorder: notify.FileRecorder{Dir: filepath.Join(dataDir, "notifications")},
}
result, err := n.Notify(ctx, "device.offline",
    map[string]string{"device": "Example device"},
    notify.Options{Key: "incident-42:open"})
```

`Notify` validates the exact variable set and returns `DeliveryID`. Omitted
users select `@administrators`. Users are event metadata; the App resolves its
configured destinations and recipients. The event and App-supplied key define
the throttle scope. Zero throttle selects ten minutes. Use different keys for
opening, recovery, escalation and independently actionable transitions.

`FileRecorder` atomically records events across CGI processes, preserves the
original delivery ID during the throttle interval, and rejects changed content
under an existing ID. It stores at most 4,096 records and 8 MiB, fails closed on
corruption or capacity, and never expires retry identities. Keep its directory
private to the package account; plan retention/export before it fills.
`Throttled` means another admission in the same interval supplied the returned
ID. A retry with the original ID returns that ID without throttling.

Apps with a transactional outbox implement `notify.Recorder` instead, admitting
the event and its outbox rows in the same transaction as the business event.
The interface requires atomic event+key throttling, immutable ID/content
binding and stable retry IDs. Admission does not perform network I/O.
`FileRecorder` is an event log, and does not create a business transaction or
schedule deliveries. A failed admission must not trigger a send.

## Send one pinned destination

Persist the delivery ID, payload, channel and exact destination revision before
network I/O. `notify.NewDeliveryID()` lets an outbox reserve the identity before
admission. Use a distinct delivery ID for each destination. Keep endpoint,
recipient list and credential reference pinned to that revision; resolve the
current secret value through `notify.Secrets` at attempt time.

```go
sender := notify.Sender{
    Destination: notify.Destination{
        Revision: "revision-1",
        Channel: "webhook",
        Endpoint: "https://receiver.invalid/notifications",
        Profile: "generic-json-v1",
    },
    Secrets: secrets,
}
receipt, err := sender.Notify(ctx, notify.Message{
    DeliveryID: deliveryID,
    DestinationRevision: "revision-1",
    Channel: "webhook",
    Payload: payload,
})
```

`Sender.Notify` attempts exactly one channel. Save every receipt, including
receipts returned with an error. Omitted delivery IDs are generated and returned
even on failure; durable workers reserve and persist them before calling.
Each destination is independent, so one failed attempt must not stop other
destinations. The App owns bounded retries, leases, ordering and reconciliation.
There is no automatic retry inside `Sender`.

- HTTPS webhooks receive `Idempotency-Key` and `X-Synopkgland-Delivery-ID`, both
  equal to the delivery ID. Redirects are refused. Optional bearer credentials
  come from `SecretRef`. Endpoints cannot embed credentials, query or fragment.
- `generic-json-v1` sends the exact JSON payload. `synology-chat-v1` uses the
  incoming webhook form protocol with an in-memory token and checks the
  application's success response. A profile is not a promise that arbitrary
  Slack, Discord or other receiver URLs accept the payload.
- SMTP requires TLS or STARTTLS, uses
  `Message-ID: <sha256-of-delivery-id@notifications.invalid>` (available as
  `notify.SMTPMessageID`), and retains that identity on
  retries. Supply `Prior: receipt.Recipients` to omit recipients already accepted
  or permanently rejected. Keep these outcomes scoped to the same delivery ID
  and destination revision.
- Attempts have a 40-second deadline, shortened by the caller's context.
  Payloads are valid JSON of at most 1 MiB. SMTP carries that JSON as UTF-8 text.
  Errors expose safe codes, retryability and a parsed HTTP `Retry-After`, without
  transport reply text, endpoint URLs or secrets.

`handed-off` means the receiver or SMTP server accepted the attempt. It does
not prove human delivery or reading. `PossiblyDuplicate` flags an unknown
outcome, such as a lost HTTP response, a server failure after possible side
effects, or a lost SMTP DATA acceptance reply. Preserve that flag in the
outbox, including across later successful attempts. A fixed ID helps receivers
deduplicate; headers and SMTP Message-ID do not guarantee exactly-once delivery.
After a process crash during sending, the App marks the outcome unknown and
retries the same ID according to its outbox policy.

Manifest generation, notification routing/settings UI, in-App bells and DSM
desktop notifications belong to their respective App/build integrations.
This package provides event admission and channel delivery primitives.
