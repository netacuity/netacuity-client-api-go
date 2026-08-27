# NetAcuity Client API — Go

Go client library for querying the [NetAcuity](https://www.digitalelement.com/solutions/netacuity/) Server for IP geolocation and intelligence data using the XML UDP query protocol.

## Requirements

- **Go** 1.18 or later
- A running **NetAcuity Server** accessible on UDP port 5400
- An **API ID** (customer-provided integer, range 0–127; default 0)

## Installation / Build

```sh
go get github.com/netacuity/netacuity-client-api-go
```

## Quick Start

The XML UDP protocol supports multiple feature codes in a single query.

```go
import (
    "context"
    "fmt"
    "log"
    "net"
    "time"
    netacuity "github.com/netacuity/netacuity-client-api-go"
)

client, err := netacuity.NewClient(
    net.ParseIP("192.0.2.1"), // NetAcuity Server IP
    81,                        // API ID (0–127, default 0)
    3*time.Second,             // default query timeout
)
if err != nil {
    log.Fatal(err)
}

result, err := client.QueryXML(
    context.Background(),
    net.ParseIP("198.51.100.1"), // IP address to look up
    []int{3, 4},                  // feature codes to query
)
if err != nil {
    log.Fatal(err)
}
for _, field := range result.Keys() { // *XMLFields — Keys() in wire order, Get(field) by name
    fmt.Printf("%s = %s\n", field, result.Get(field))
}
```

## API Reference

### NewClient / NewClientWithDefaultTimeout / NewClientWithDefaultAPIID

```go
type Client struct {
    ServerIP net.IP
    Timeout  time.Duration // default/fallback network deadline, used when a call's context has none
    // API ID is unexported; set it via NewClient/NewClientWithDefaultTimeout or SetAPIID.
}

func NewClient(serverIP net.IP, apiID int, timeout time.Duration) (*Client, error)
func NewClientWithDefaultTimeout(serverIP net.IP, apiID int) (*Client, error) // uses DefaultTimeout (2s)
func NewClientWithDefaultAPIID(serverIP net.IP, timeout time.Duration) *Client // uses apiID 0

func (c *Client) SetAPIID(id int) error
```

Every query is a method on `*Client`. A `Client` is safe to construct once and reuse
across many queries against the same server/API ID.

### Client.QueryXML

```go
func (c *Client) QueryXML(ctx context.Context, queryIP net.IP, featureCodes []int) (*XMLFields, error)
```

Queries the NetAcuity Server using the XML UDP protocol. Returns a `*XMLFields`
holding all fields from the requested feature codes. Multi-packet responses are
reassembled automatically. Both IPv4 and IPv6 are supported. A transaction ID is
generated automatically for each call.

### Client.QueryXMLWithTransactionID

```go
func (c *Client) QueryXMLWithTransactionID(ctx context.Context, queryIP net.IP, featureCodes []int, transactionID string) (*XMLFields, error)
```

Same as `QueryXML`, but lets the caller supply their own `transactionID` instead of
having one generated automatically — useful for correlating a query with an external
log or trace. `transactionID` must not contain `"`, `<`, `>`, or `&` (it is embedded in
the request's `trans-id` XML attribute); a `transactionID` containing one of those
characters returns an error rather than being sent.

On a rejected-but-received response (transaction-ID/IP mismatch or a DB-level error), `err` is a `*netacuity.RawResponseError` — recover its `RawResponse` field via `errors.As` for diagnostics.

### Parameters

| Parameter | Type | Description |
|---|---|---|
| `serverIP` | `net.IP` | Your NetAcuity Server IP address |
| `apiID` | `int` | Your API ID (0–127), client-provided, default 0 |
| `timeout` | `time.Duration` | Query timeout (must be > 0), used as a ceiling on the effective deadline |
| `ctx` | `context.Context` | Per-call context; the effective network deadline is whichever comes first, `ctx`'s deadline (if any) or `Client.Timeout` — a long-lived `ctx` cannot extend the configured timeout |
| `queryIP` | `net.IP` | The IP address to look up |
| `featureCodes` / `featureCode` | `[]int` / `int` | Feature code(s) to query |

## Feature Codes

For the complete, up-to-date list of feature codes and their response fields, see the [NetAcuity documentation](https://docs.netacuity.com/). Response fields are returned as name/value pairs via `*XMLFields` — see [Client.QueryXML](#clientqueryxml) above.

## IPv6

`Client.QueryXML` supports both IPv4 and IPv6. When querying via IPv6, pass a
global-facing IPv6 address as `queryIP` — link-local addresses are not routable and
will not return results.

## Examples

Runnable examples are provided in the `examples/` directory:

```sh
# One or more feature codes — XML UDP protocol (comma-separated)
go run ./examples/xml/main.go <server-ip> <query-ip> <feature-code>[,<feature-code>...]
```

## Running the Tests

The test suite covers input validation and response parsing without requiring a live server:

```sh
go test -v .
```

Integration tests against a real NetAcuity Server are not included.

## Changelog

See [CHANGELOG.md](CHANGELOG.md) for release history.

## Support

Technical Support is only available to those under active contract with Digital Element. To contact Support, use the contact information provided at contract initiation.

- Documentation: [docs.netacuity.com](https://docs.netacuity.com/)
- Issues: [GitHub Issues](https://github.com/netacuity/netacuity-client-api-go/issues)

## License

Copyright 2026 Digital Envoy, Inc.

Licensed under the Apache License, Version 2.0. See [LICENSE](LICENSE) for the full license text.

This repository contains no third-party source code or binaries, and this module has no external dependencies — every import resolves to the Go standard library.
