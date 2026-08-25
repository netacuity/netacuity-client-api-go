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

package netacuity

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// mockUDPServer is a minimal UDP server for exercising the real network path
// (net.DialUDP / connection.Write / connection.Read) that QueryXML uses, without
// depending on a live NetAcuity server. It binds an OS-assigned ephemeral port and
// replies to every datagram it receives with a pre-configured byte sequence.
type mockUDPServer struct {
	conn *net.UDPConn

	mu       sync.Mutex
	response []byte
}

// newMockUDPServer starts the server and registers its shutdown via t.Cleanup.
func newMockUDPServer(t *testing.T) *mockUDPServer {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("mock UDP server: listen: %v", err)
	}
	s := &mockUDPServer{conn: conn}
	go s.loop()
	t.Cleanup(func() { s.conn.Close() })
	return s
}

// setResponse configures the byte sequence sent back for every datagram received
// from here on.
func (s *mockUDPServer) setResponse(b []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.response = b
}

// addr returns the "127.0.0.1:<port>" address the server is listening on.
func (s *mockUDPServer) addr() string {
	return s.conn.LocalAddr().String()
}

func (s *mockUDPServer) loop() {
	buf := make([]byte, MaxResponseSize)
	for {
		_, remote, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			// conn.Close() (via t.Cleanup) unblocks ReadFromUDP with an error; exit quietly.
			return
		}
		s.mu.Lock()
		resp := s.response
		s.mu.Unlock()
		if resp != nil {
			_, _ = s.conn.WriteToUDP(resp, remote)
		}
	}
}

// ─── checkAPIID ────────────────────────────────────────────────────────────────

func TestCheckAPIID(t *testing.T) {
	tests := []struct {
		apiID int
		want  bool
	}{
		{0, true},
		{1, true},
		{64, true},
		{126, true},
		{127, true},
		{-1, false},
		{128, false},
		{-100, false},
		{1000, false},
		{255, false},
	}
	for _, tc := range tests {
		if got := checkAPIID(tc.apiID); got != tc.want {
			t.Errorf("checkAPIID(%d) = %v, want %v", tc.apiID, got, tc.want)
		}
	}
}

// ─── generateTransactionID ─────────────────────────────────────────────────────

func TestGenerateTransactionID_Length(t *testing.T) {
	for _, length := range []int{1, 5, 10, 20, 50} {
		id := generateTransactionID(length)
		if len(id) != length {
			t.Errorf("generateTransactionID(%d) returned length %d, want %d", length, len(id), length)
		}
	}
}

func TestGenerateTransactionID_Alphanumeric(t *testing.T) {
	id := generateTransactionID(200)
	for _, c := range id {
		isLower := c >= 'a' && c <= 'z'
		isUpper := c >= 'A' && c <= 'Z'
		isDigit := c >= '0' && c <= '9'
		if !isLower && !isUpper && !isDigit {
			t.Errorf("generateTransactionID produced non-alphanumeric character: %q (0x%x)", c, c)
		}
	}
}

func TestGenerateTransactionID_Uniqueness(t *testing.T) {
	// generateTransactionID draws from crypto/rand — so with a 62-char alphabet
	// and length 10, a collision across 100 draws is astronomically unlikely
	// (~1e-15). All 100 should be unique.
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := generateTransactionID(TransactionLength)
		seen[id] = true
	}
	if len(seen) != 100 {
		t.Errorf("generateTransactionID produced only %d unique ID(s) out of 100 calls", len(seen))
	}
}

func TestGenerateTransactionID_ZeroLength(t *testing.T) {
	id := generateTransactionID(0)
	if id != "" {
		t.Errorf("generateTransactionID(0) = %q, want empty string", id)
	}
}

// ─── parseXmlResponse ─────────────────────────────────────────────────────────

func TestParseXmlResponse_Basic(t *testing.T) {
	xmlStr := `<response trans-id="abc123" ip="192.0.2.2" country="usa" />`
	result, err := parseXmlResponse(xmlStr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Get("trans-id") != "abc123" {
		t.Errorf("trans-id = %q, want abc123", result.Get("trans-id"))
	}
	if result.Get("ip") != "192.0.2.2" {
		t.Errorf("ip = %q, want 192.0.2.2", result.Get("ip"))
	}
	if result.Get("country") != "usa" {
		t.Errorf("country = %q, want usa", result.Get("country"))
	}
}

func TestParseXmlResponse_ManyAttributes(t *testing.T) {
	xmlStr := `<response trans-id="xyz" ip="192.0.2.3" country="can" region="on" city="toronto" lat="43.7" lon="-79.4" />`
	result, err := parseXmlResponse(xmlStr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := map[string]string{
		"trans-id": "xyz",
		"ip":       "192.0.2.3",
		"country":  "can",
		"region":   "on",
		"city":     "toronto",
		"lat":      "43.7",
		"lon":      "-79.4",
	}
	for k, v := range expected {
		if result.Get(k) != v {
			t.Errorf("result.Get(%q) = %q, want %q", k, result.Get(k), v)
		}
	}
}

func TestParseXmlResponse_KeysPreservesWireOrder(t *testing.T) {
	xmlStr := `<response trans-id="xyz" ip="192.0.2.3" country="can" region="on" />`
	result, err := parseXmlResponse(xmlStr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"trans-id", "ip", "country", "region"}
	got := result.Keys()
	if len(got) != len(want) {
		t.Fatalf("Keys() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Keys()[%d] = %q, want %q (Keys() = %v)", i, got[i], want[i], got)
		}
	}
}

func TestParseXmlResponse_NoAttributes(t *testing.T) {
	xmlStr := `<response />`
	result, err := parseXmlResponse(xmlStr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Keys()) != 0 {
		t.Errorf("expected no fields, got %d entries: %v", len(result.Keys()), result.Keys())
	}
}

func TestParseXmlResponse_NonSelfClosing(t *testing.T) {
	xmlStr := `<response trans-id="t1" ip="198.51.100.2"></response>`
	result, err := parseXmlResponse(xmlStr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Get("trans-id") != "t1" {
		t.Errorf("trans-id = %q, want t1", result.Get("trans-id"))
	}
}

func TestParseXmlResponse_EmptyString(t *testing.T) {
	_, err := parseXmlResponse("")
	if err == nil {
		t.Error("expected error for empty string, got nil")
	}
}

func TestParseXmlResponse_InvalidXml(t *testing.T) {
	_, err := parseXmlResponse("<unclosed")
	if err == nil {
		t.Error("expected error for malformed XML, got nil")
	}
}

func TestParseXmlResponse_XmlDeclarationFirst(t *testing.T) {
	// The XML declaration is a ProcInst token, not a StartElement — should error.
	_, err := parseXmlResponse(`<?xml version="1.0"?><response ip="192.0.2.2" />`)
	if err == nil {
		t.Error("expected error when XML declaration precedes root element, got nil")
	}
}

func TestParseXmlResponse_AttributeWithSpecialChars(t *testing.T) {
	xmlStr := `<response city="d'arcy" region="british columbia" country="can" />`
	result, err := parseXmlResponse(xmlStr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Get("city") != "d'arcy" {
		t.Errorf("city = %q, want d'arcy", result.Get("city"))
	}
	if result.Get("region") != "british columbia" {
		t.Errorf("region = %q, want british columbia", result.Get("region"))
	}
	if result.Get("country") != "can" {
		t.Errorf("country = %q, want can", result.Get("country"))
	}
}

func TestParseXmlResponse_AttributeWithEntities(t *testing.T) {
	xmlStr := `<response name="AT&amp;T" />`
	result, err := parseXmlResponse(xmlStr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Get("name") != "AT&T" {
		t.Errorf("name = %q, want AT&T", result.Get("name"))
	}
}

// ─── Client.deadline ──────────────────────────────────────────────────────

func TestDeadline_ContextDeadlineShorterThanTimeout_Wins(t *testing.T) {
	c := &Client{Timeout: 10 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	got := c.deadline(ctx)
	limit := time.Now().Add(2 * time.Second) // comfortably inside ctx's ~1s deadline, well under the 10s client timeout
	if !got.Before(limit) {
		t.Errorf("deadline() = %v, want a deadline near ctx's 1s timeout (before %v)", got, limit)
	}
}

func TestDeadline_ContextDeadlineLongerThanTimeout_DoesNotWin(t *testing.T) {
	c := &Client{Timeout: 1 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	got := c.deadline(ctx)
	limit := time.Now().Add(2 * time.Second) // comfortably inside the 1s client timeout, well under ctx's 30s deadline
	if !got.Before(limit) {
		t.Errorf("deadline() = %v, want it bounded by Client.Timeout (1s), not ctx's 30s deadline", got)
	}
}

// ─── QueryXML — input validation (no network required) ────────────────────────

func TestQueryXML_NilServerIp(t *testing.T) {
	client, err := NewClient(nil, 64, 3*time.Second)
	if err != nil {
		t.Fatalf("unexpected error constructing client: %v", err)
	}
	_, err = client.QueryXML(context.Background(), net.ParseIP("192.0.2.2"), []int{3})
	if err == nil {
		t.Fatal("expected error for nil server IP, got nil")
	}
	if !strings.Contains(err.Error(), "server IP") {
		t.Errorf("error %q should mention server IP", err.Error())
	}
}

func TestQueryXML_NilQueryIp(t *testing.T) {
	client, err := NewClient(net.ParseIP("192.0.2.1"), 64, 3*time.Second)
	if err != nil {
		t.Fatalf("unexpected error constructing client: %v", err)
	}
	_, err = client.QueryXML(context.Background(), nil, []int{3})
	if err == nil {
		t.Fatal("expected error for nil query IP, got nil")
	}
	if !strings.Contains(err.Error(), "query IP") {
		t.Errorf("error %q should mention query IP", err.Error())
	}
}

func TestQueryXML_InvalidApiId_Negative(t *testing.T) {
	// apiID validation now happens at construction time (NewClient), before any query.
	_, err := NewClient(net.ParseIP("192.0.2.1"), -1, 3*time.Second)
	if err == nil {
		t.Fatal("expected error for negative API ID, got nil")
	}
	if !strings.Contains(err.Error(), "API ID") {
		t.Errorf("error %q should mention API ID", err.Error())
	}
}

func TestQueryXML_InvalidApiId_TooLarge(t *testing.T) {
	_, err := NewClient(net.ParseIP("192.0.2.1"), 128, 3*time.Second)
	if err == nil {
		t.Fatal("expected error for API ID 128, got nil")
	}
}

func TestQueryXML_EmptyFeatureCodes(t *testing.T) {
	client, err := NewClient(net.ParseIP("192.0.2.1"), 64, 3*time.Second)
	if err != nil {
		t.Fatalf("unexpected error constructing client: %v", err)
	}
	_, err = client.QueryXML(context.Background(), net.ParseIP("192.0.2.2"), []int{})
	if err == nil {
		t.Fatal("expected error for empty feature codes slice, got nil")
	}
	if !strings.Contains(err.Error(), "Feature-Code") {
		t.Errorf("error %q should mention Feature-Code", err.Error())
	}
}

func TestQueryXML_NilFeatureCodes(t *testing.T) {
	client, err := NewClient(net.ParseIP("192.0.2.1"), 64, 3*time.Second)
	if err != nil {
		t.Fatalf("unexpected error constructing client: %v", err)
	}
	_, err = client.QueryXML(context.Background(), net.ParseIP("192.0.2.2"), nil)
	if err == nil {
		t.Fatal("expected error for nil feature codes slice, got nil")
	}
}

func TestQueryXML_NegativeFeatureCode(t *testing.T) {
	client, err := NewClient(net.ParseIP("192.0.2.1"), 64, 3*time.Second)
	if err != nil {
		t.Fatalf("unexpected error constructing client: %v", err)
	}
	_, err = client.QueryXML(context.Background(), net.ParseIP("192.0.2.2"), []int{3, -1, 8})
	if err == nil {
		t.Fatal("expected error for negative feature code in slice, got nil")
	}
	if !strings.Contains(err.Error(), "Feature-Code") {
		t.Errorf("error %q should mention Feature-Code", err.Error())
	}
}

func TestQueryXML_NegativeFeatureCode_FirstElement(t *testing.T) {
	client, err := NewClient(net.ParseIP("192.0.2.1"), 64, 3*time.Second)
	if err != nil {
		t.Fatalf("unexpected error constructing client: %v", err)
	}
	_, err = client.QueryXML(context.Background(), net.ParseIP("192.0.2.2"), []int{-5})
	if err == nil {
		t.Fatal("expected error for negative feature code, got nil")
	}
}

func TestQueryXML_InvalidTimeout_Zero(t *testing.T) {
	client, err := NewClient(net.ParseIP("192.0.2.1"), 64, 0)
	if err != nil {
		t.Fatalf("unexpected error constructing client: %v", err)
	}
	_, err = client.QueryXML(context.Background(), net.ParseIP("192.0.2.2"), []int{3})
	if err == nil {
		t.Fatal("expected error for zero timeout, got nil")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Errorf("error %q should mention timeout", err.Error())
	}
}

func TestQueryXML_InvalidTimeout_Negative(t *testing.T) {
	client, err := NewClient(net.ParseIP("192.0.2.1"), 64, -1*time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected error constructing client: %v", err)
	}
	_, err = client.QueryXML(context.Background(), net.ParseIP("192.0.2.2"), []int{3})
	if err == nil {
		t.Fatal("expected error for negative timeout, got nil")
	}
}

func TestQueryXML_ValidApiId_Boundaries(t *testing.T) {
	for _, apiID := range []int{0, 127} {
		client, err := NewClient(net.ParseIP("192.0.2.1"), apiID, time.Nanosecond)
		if err != nil {
			t.Errorf("apiID %d should be valid, but NewClient returned: %v", apiID, err)
			continue
		}
		_, err = client.QueryXML(context.Background(), net.ParseIP("192.0.2.2"), []int{3})
		if err != nil && strings.Contains(err.Error(), "API ID") {
			t.Errorf("apiID %d should be valid, but got API ID validation error: %v", apiID, err)
		}
	}
}

// ─── Caller-supplied transaction ID — successful round-trip echo ──────────────

func TestParseXmlResponse_CallerSuppliedTransactionID_Echoed(t *testing.T) {
	// Mirrors TestParseXmlResponse_Basic, but with a caller-chosen transaction ID.
	xmlStr := `<response trans-id="my-custom-txn-42" ip="192.0.2.2" country="usa" />`
	result, err := parseXmlResponse(xmlStr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Get("trans-id") != "my-custom-txn-42" {
		t.Errorf("trans-id = %q, want my-custom-txn-42", result.Get("trans-id"))
	}
}

// ─── QueryXMLWithTransactionID — input validation (no network required) ───────

func TestQueryXMLWithTransactionID_RejectsDoubleQuote(t *testing.T) {
	client, err := NewClient(net.ParseIP("192.0.2.1"), 64, 3*time.Second)
	if err != nil {
		t.Fatalf("unexpected error constructing client: %v", err)
	}
	_, err = client.QueryXMLWithTransactionID(context.Background(), net.ParseIP("192.0.2.2"), []int{3}, `abc"123`)
	if err == nil {
		t.Fatal(`expected error for transaction ID containing '"', got nil`)
	}
	if !strings.Contains(err.Error(), "transactionID") {
		t.Errorf("error %q should mention transactionID", err.Error())
	}
}

func TestQueryXMLWithTransactionID_RejectsLessThan(t *testing.T) {
	client, err := NewClient(net.ParseIP("192.0.2.1"), 64, 3*time.Second)
	if err != nil {
		t.Fatalf("unexpected error constructing client: %v", err)
	}
	_, err = client.QueryXMLWithTransactionID(context.Background(), net.ParseIP("192.0.2.2"), []int{3}, "abc<123")
	if err == nil {
		t.Fatal("expected error for transaction ID containing '<', got nil")
	}
	if !strings.Contains(err.Error(), "transactionID") {
		t.Errorf("error %q should mention transactionID", err.Error())
	}
}

func TestQueryXMLWithTransactionID_RejectsGreaterThan(t *testing.T) {
	client, err := NewClient(net.ParseIP("192.0.2.1"), 64, 3*time.Second)
	if err != nil {
		t.Fatalf("unexpected error constructing client: %v", err)
	}
	_, err = client.QueryXMLWithTransactionID(context.Background(), net.ParseIP("192.0.2.2"), []int{3}, "abc>123")
	if err == nil {
		t.Fatal("expected error for transaction ID containing '>', got nil")
	}
	if !strings.Contains(err.Error(), "transactionID") {
		t.Errorf("error %q should mention transactionID", err.Error())
	}
}

func TestQueryXMLWithTransactionID_RejectsAmpersand(t *testing.T) {
	client, err := NewClient(net.ParseIP("192.0.2.1"), 64, 3*time.Second)
	if err != nil {
		t.Fatalf("unexpected error constructing client: %v", err)
	}
	_, err = client.QueryXMLWithTransactionID(context.Background(), net.ParseIP("192.0.2.2"), []int{3}, "abc&123")
	if err == nil {
		t.Fatal("expected error for transaction ID containing '&', got nil")
	}
	if !strings.Contains(err.Error(), "transactionID") {
		t.Errorf("error %q should mention transactionID", err.Error())
	}
}

func TestQueryXMLWithTransactionID_ValidIdPassesValidation(t *testing.T) {
	// A transaction ID with none of '"', '<', '>', '&' should pass validation — any
	// resulting error must come from the (unreachable in this test) network stage.
	client, err := NewClient(net.ParseIP("192.0.2.1"), 64, time.Nanosecond)
	if err != nil {
		t.Fatalf("unexpected error constructing client: %v", err)
	}
	_, err = client.QueryXMLWithTransactionID(context.Background(), net.ParseIP("192.0.2.2"), []int{3}, "abc-123_XYZ")
	if err != nil && strings.Contains(err.Error(), "transactionID") {
		t.Errorf("valid transaction ID should not fail validation, got: %v", err)
	}
}

// ─── Constants ────────────────────────────────────────────────────────────────

func TestConstants(t *testing.T) {
	if NAProtocolAPIVersion != 5 {
		t.Errorf("NAProtocolAPIVersion = %d, want 5", NAProtocolAPIVersion)
	}
	if NALanguageAPIType != 31 {
		t.Errorf("NALanguageAPIType = %d, want 31", NALanguageAPIType)
	}
	if NetacuityPort != 5400 {
		t.Errorf("NetacuityPort = %d, want 5400", NetacuityPort)
	}
	if MaxResponseSize != 1500 {
		t.Errorf("MaxResponseSize = %d, want 1500", MaxResponseSize)
	}
	if TransactionLength != 10 {
		t.Errorf("TransactionLength = %d, want 10", TransactionLength)
	}
	if UDP4 != "udp4" {
		t.Errorf("UDP4 = %q, want udp4", UDP4)
	}
	if UDP6 != "udp6" {
		t.Errorf("UDP6 = %q, want udp6", UDP6)
	}
}

// ─── Real UDP round trip (mockUDPServer) ───────────────────────────────────────

func TestQueryXML_RealUDPRoundTrip_Success(t *testing.T) {
	server := newMockUDPServer(t)
	queryIP := net.ParseIP("192.0.2.10")
	txnID := "roundtrip2"
	xmlContent := `<response ip="` + queryIP.String() + `" trans-id="` + txnID + `" geo-country="usa" />`
	// Wire format is "PPTT" (packet/total, 2 digits each) + XML content + 1 trailing
	// byte that queryXMLAtAddr strips via tempBuffer[4:size-1] (mirrors the real
	// server always terminating each packet with an extra byte after the XML).
	server.setResponse([]byte("0101" + xmlContent + "\x00"))

	result, err := queryXMLAtAddr([]int{3}, 0, queryIP, server.addr(), "udp4", time.Second, txnID, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Get("geo-country") != "usa" {
		t.Errorf(`result.Get("geo-country") = %q, want "usa"`, result.Get("geo-country"))
	}
	if result.Get("ip") != queryIP.String() {
		t.Errorf(`result.Get("ip") = %q, want %q`, result.Get("ip"), queryIP.String())
	}
}

func TestQueryXML_RealUDPRoundTrip_MalformedXml(t *testing.T) {
	// Promotes TestParseXmlResponse_InvalidXml into a real socket round trip.
	server := newMockUDPServer(t)
	queryIP := net.ParseIP("192.0.2.22")
	txnID := "malformedxml1"
	server.setResponse([]byte("0101<unclosed\x00"))

	_, err := queryXMLAtAddr([]int{3}, 0, queryIP, server.addr(), "udp4", time.Second, txnID, nil)
	if err == nil {
		t.Fatal("expected error for malformed XML received over a real socket, got nil")
	}
}

func TestQueryXML_RealUDPRoundTrip_RejectsIPMismatch(t *testing.T) {
	server := newMockUDPServer(t)
	queryIP := net.ParseIP("192.0.2.23")
	txnID := "xmlmismatch1"
	xmlContent := `<response ip="203.0.113.9" trans-id="` + txnID + `" geo-country="usa" />`
	server.setResponse([]byte("0101" + xmlContent + "\x00"))

	_, err := queryXMLAtAddr([]int{3}, 0, queryIP, server.addr(), "udp4", time.Second, txnID, nil)
	if err == nil {
		t.Fatal("expected error for response IP mismatch, got nil")
	}
	if !strings.Contains(err.Error(), "address mismatch") {
		t.Errorf("error = %q, want it to mention address mismatch", err.Error())
	}
	var rawErr *RawResponseError
	if !errors.As(err, &rawErr) {
		t.Fatal("expected err to be a *RawResponseError, so the raw response stays available for diagnostics")
	}
}

func TestQueryXML_RealUDPRoundTrip_RejectsTransactionIDMismatch(t *testing.T) {
	server := newMockUDPServer(t)
	queryIP := net.ParseIP("192.0.2.24")
	txnID := "xmlmismatch2"
	xmlContent := `<response ip="` + queryIP.String() + `" trans-id="not-the-txn-id" geo-country="usa" />`
	server.setResponse([]byte("0101" + xmlContent + "\x00"))

	_, err := queryXMLAtAddr([]int{3}, 0, queryIP, server.addr(), "udp4", time.Second, txnID, nil)
	if err == nil {
		t.Fatal("expected error for transaction ID mismatch, got nil")
	}
	if !strings.Contains(err.Error(), "transaction ID mismatch") {
		t.Errorf("error = %q, want it to mention transaction ID mismatch", err.Error())
	}
	var rawErr *RawResponseError
	if !errors.As(err, &rawErr) {
		t.Fatal("expected err to be a *RawResponseError, so the raw response stays available for diagnostics")
	}
}

func TestQueryXML_RealUDPRoundTrip_ErrorAttribute(t *testing.T) {
	server := newMockUDPServer(t)
	queryIP := net.ParseIP("192.0.2.25")
	txnID := "xmlerr1"
	xmlContent := `<response ip="` + queryIP.String() + `" trans-id="` + txnID + `" error="DB Not Loaded" />`
	server.setResponse([]byte("0101" + xmlContent + "\x00"))

	_, err := queryXMLAtAddr([]int{3}, 0, queryIP, server.addr(), "udp4", time.Second, txnID, nil)
	if err == nil {
		t.Fatal("expected error for a DB-level error attribute, got nil")
	}
	if !strings.Contains(err.Error(), "DB Not Loaded") {
		t.Errorf("error = %q, want it to mention the DB-level error message", err.Error())
	}
	var rawErr *RawResponseError
	if !errors.As(err, &rawErr) {
		t.Fatal("expected err to be a *RawResponseError, so the raw response stays available for diagnostics")
	}
}

// ─── ValidationError ────────────────────────────────────────────────────────────

func TestValidationError_Error(t *testing.T) {
	err := &ValidationError{Msg: "invalid API ID"}
	if err.Error() != "invalid API ID" {
		t.Errorf("Error() = %q, want %q", err.Error(), "invalid API ID")
	}
}

// ─── NewClient / NewClientWithDefaultAPIID / SetAPIID ──────────────────────────

func TestNewClient_InvalidApiId_ReturnsErrorAtConstruction(t *testing.T) {
	_, err := NewClient(net.ParseIP("192.0.2.1"), -1, 3*time.Second)
	if err == nil {
		t.Fatal("expected error for negative API ID at construction time, got nil")
	}
	var valErr *ValidationError
	if !errors.As(err, &valErr) {
		t.Errorf("err = %T, want *ValidationError", err)
	}
}

func TestNewClient_ValidApiId_Succeeds(t *testing.T) {
	c, err := NewClient(net.ParseIP("192.0.2.1"), 64, 3*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.apiID != 64 {
		t.Errorf("apiID = %d, want 64", c.apiID)
	}
}

func TestNewClientWithDefaultAPIID(t *testing.T) {
	c := NewClientWithDefaultAPIID(net.ParseIP("192.0.2.1"), 3*time.Second)
	if c.apiID != 0 {
		t.Errorf("apiID = %d, want 0", c.apiID)
	}
	if c.Timeout != 3*time.Second {
		t.Errorf("Timeout = %v, want 3s", c.Timeout)
	}
}

func TestSetAPIID_Valid(t *testing.T) {
	c := &Client{}
	if err := c.SetAPIID(100); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.apiID != 100 {
		t.Errorf("apiID = %d, want 100", c.apiID)
	}
}

func TestSetAPIID_Invalid(t *testing.T) {
	c := &Client{apiID: 5}
	if err := c.SetAPIID(200); err == nil {
		t.Fatal("expected error for API ID 200, got nil")
	}
	if c.apiID != 5 {
		t.Errorf("apiID = %d, want unchanged 5 after a rejected SetAPIID", c.apiID)
	}
}
