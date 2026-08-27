// Copyright 2026 Digital Envoy, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package netacuity provides a Go client for the NetAcuity IP geolocation API.
// Construct a Client with NewClient, NewClientWithDefaultTimeout, or
// NewClientWithDefaultAPIID, then use its QueryXML method to query one or more
// feature codes.
package netacuity

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

const (
	NAProtocolAPIVersion = 5
	NALanguageAPIType    = 31
	NetacuityPort        = 5400
	UDP6                 = "udp6"
	UDP4                 = "udp4"
	MaxResponseSize      = 1500
	TransactionLength    = 10
)

// DefaultTimeout is the timeout used by NewClientWithDefaultTimeout.
const DefaultTimeout = 2 * time.Second

// Client is a NetAcuity API client bound to a specific server, API ID, and timeout.
// Construct one with NewClient, NewClientWithDefaultTimeout, or
// NewClientWithDefaultAPIID, and reuse it across queries.
type Client struct {
	ServerIP net.IP
	apiID    int
	Timeout  time.Duration // default/fallback network deadline, used when a call's context has none

	// dialFunc creates the UDP connection used for a query; nil means net.DialUDP.
	// Tests override it to simulate a socket-creation failure without a real network.
	dialFunc func(network string, laddr, raddr *net.UDPAddr) (*net.UDPConn, error)
}

// NewClient returns a Client that queries serverIP using apiID. timeout is the
// network deadline used for calls whose context has none. apiID must be in the
// range 0-127; an out-of-range apiID returns a *ValidationError.
func NewClient(serverIP net.IP, apiID int, timeout time.Duration) (*Client, error) {
	c := &Client{ServerIP: serverIP, Timeout: timeout}
	if err := c.SetAPIID(apiID); err != nil {
		return nil, err
	}
	return c, nil
}

// NewClientWithDefaultTimeout is NewClient using DefaultTimeout.
func NewClientWithDefaultTimeout(serverIP net.IP, apiID int) (*Client, error) {
	return NewClient(serverIP, apiID, DefaultTimeout)
}

// NewClientWithDefaultAPIID is NewClient using an apiID of 0. apiID 0 is always
// within the valid range, so unlike NewClient this cannot fail.
func NewClientWithDefaultAPIID(serverIP net.IP, timeout time.Duration) *Client {
	c, _ := NewClient(serverIP, 0, timeout)
	return c
}

// SetAPIID validates id (0-127) and, if valid, sets it as the client's API ID.
// On an invalid id it returns a *ValidationError and leaves the client's API ID
// unchanged.
func (c *Client) SetAPIID(id int) error {
	if !checkAPIID(id) {
		return &ValidationError{Msg: "invalid API ID"}
	}
	c.apiID = id
	return nil
}

// deadline returns the earlier of ctx's deadline (if any) and now+c.Timeout, so a
// long-lived caller context cannot extend this client's configured timeout.
func (c *Client) deadline(ctx context.Context) time.Time {
	fallback := time.Now().Add(c.Timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(fallback) {
		return d
	}
	return fallback
}

// QueryXML queries the NetAcuity server using the XML UDP protocol for one or more
// feature codes. It supports both IPv4 and IPv6 server and query addresses.
//
// The returned *XMLFields holds field name → value pairs from all requested
// feature codes; Keys() returns them in the order the server sent them (trans-id
// and ip first). Multi-packet responses are reassembled automatically.
//
// A transaction ID is generated automatically for each call. To supply your own
// (e.g. to correlate this call with an external log or trace), use
// QueryXMLWithTransactionID instead.
func (c *Client) QueryXML(ctx context.Context, queryIP net.IP, featureCodes []int) (*XMLFields, error) {
	return c.QueryXMLWithTransactionID(ctx, queryIP, featureCodes, generateTransactionID(TransactionLength))
}

// QueryXMLWithTransactionID is QueryXML, but with a caller-supplied transactionID
// instead of one generated automatically. The server echoes the transaction ID back
// in its response, and QueryXMLWithTransactionID validates that the echoed value
// matches transactionID before returning.
//
// transactionID must not contain '"', '<', '>', or '&' — it is embedded directly into
// the trans-id attribute of the XML UDP request, and any of those characters would
// break out of the attribute value or otherwise corrupt the request. A transactionID
// containing one of those characters returns an error rather than being sent.
func (c *Client) QueryXMLWithTransactionID(ctx context.Context, queryIP net.IP, featureCodes []int, transactionID string) (*XMLFields, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.ServerIP == nil {
		return nil, &ValidationError{Msg: "invalid server IP address"}
	}
	if queryIP == nil {
		return nil, &ValidationError{Msg: "invalid query IP address"}
	}
	if !checkAPIID(c.apiID) {
		return nil, &ValidationError{Msg: "invalid API ID"}
	}
	if len(featureCodes) == 0 {
		return nil, &ValidationError{Msg: "invalid array of Feature-Codes"}
	}
	for _, featureCode := range featureCodes {
		if featureCode < 3 || featureCode >= 100 {
			return nil, fmt.Errorf("invalid Feature-Code: %d", featureCode)
		}
	}
	timeout := time.Until(c.deadline(ctx))
	if timeout <= 0 {
		return nil, &ValidationError{Msg: "invalid timeout delay"}
	}
	if strings.ContainsAny(transactionID, `"<>&`) {
		return nil, &ValidationError{Msg: `transactionID must not contain '"', '<', '>', or '&'`}
	}

	var serverIPPort string
	var protocol string

	if c.ServerIP.To4() == nil {
		serverIPPort = fmt.Sprintf("[%s]:%d", c.ServerIP, NetacuityPort)
		protocol = UDP6
	} else {
		serverIPPort = fmt.Sprintf("%s:%d", c.ServerIP, NetacuityPort)
		protocol = UDP4
	}

	return queryXMLAtAddr(featureCodes, c.apiID, queryIP, serverIPPort, protocol, timeout, transactionID, c.dialFunc)
}

// queryXMLAtAddr performs the XML UDP request/response round trip against addr
// (host:port), using protocol ("udp4" or "udp6"). Factored out of
// Client.QueryXMLWithTransactionID — which always targets NetacuityPort on the
// client's ServerIP — so tests can point the real net.DialUDP/Read/Write path at a
// local mock UDP server instead of a live NetAcuity server. dial is used in place of
// net.DialUDP when non-nil, so tests can also force a socket-creation failure.
func queryXMLAtAddr(featureCodes []int, apiID int, queryIP net.IP, addr string, protocol string, timeout time.Duration, transactionID string, dial func(network string, laddr, raddr *net.UDPAddr) (*net.UDPConn, error)) (*XMLFields, error) {
	tempBuffer := make([]byte, MaxResponseSize)

	if dial == nil {
		dial = net.DialUDP
	}

	serverAddress, err := net.ResolveUDPAddr(protocol, addr)
	if err != nil {
		return nil, err
	}
	connection, err := dial(protocol, nil, serverAddress)
	if err != nil {
		return nil, err
	}
	connection.SetDeadline(time.Now().Add(timeout))
	defer connection.Close()

	udpMessage := fmt.Sprintf(`<request trans-id="%s" ip="%v" api-id="%d">`, transactionID, queryIP, apiID)
	for _, code := range featureCodes {
		udpMessage += fmt.Sprintf(` <query db="%d" />`, code)
	}
	udpMessage += " </request>"
	if _, err := connection.Write([]byte(udpMessage)); err != nil {
		return nil, err
	}

	result := ""
	previousPacket := 0
	currentPacket := 0
	totalPackets := 1
	for currentPacket < totalPackets {
		tempBuffer = make([]byte, MaxResponseSize)
		size, err := connection.Read(tempBuffer[:])
		if err != nil {
			return nil, err
		}
		if size < 5 {
			return nil, fmt.Errorf("malformed response packet: got %d bytes, need at least 5", size)
		}
		currentPacket, err = strconv.Atoi(string(tempBuffer[0:2]))
		if err != nil {
			return nil, fmt.Errorf("malformed response packet: invalid packet number: %w", err)
		}
		totalPackets, err = strconv.Atoi(string(tempBuffer[2:4]))
		if err != nil {
			return nil, fmt.Errorf("malformed response packet: invalid packet count: %w", err)
		}
		if currentPacket-1 != previousPacket {
			return nil, &ValidationError{Msg: "error: packets received out of order"}
		}
		previousPacket = currentPacket
		result += string(tempBuffer[4 : size-1])
	}

	responseFields, err := parseXmlResponse(result)
	if err != nil {
		return nil, err
	}

	// Verify the response actually answers this request before trusting its fields:
	// UDP has no handshake, so an unrelated or spoofed packet must not be accepted.
	if responseIP := net.ParseIP(responseFields.Get("ip")); responseIP == nil || !responseIP.Equal(queryIP) {
		return nil, &RawResponseError{
			Err:         fmt.Errorf("response address mismatch: got %q, want %q", responseFields.Get("ip"), queryIP.String()),
			RawResponse: result,
		}
	}
	if responseFields.Get("trans-id") != transactionID {
		return nil, &RawResponseError{
			Err:         fmt.Errorf("response transaction ID mismatch: got %q, want %q", responseFields.Get("trans-id"), transactionID),
			RawResponse: result,
		}
	}
	if errMsg := responseFields.Get("error"); errMsg != "" {
		return nil, &RawResponseError{
			Err:         errors.New(errMsg),
			RawResponse: result,
		}
	}

	return responseFields, nil
}
