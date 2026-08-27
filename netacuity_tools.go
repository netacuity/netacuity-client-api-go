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
	"crypto/rand"
	"encoding/xml"
	"errors"
	"strings"
)

// RawResponseError wraps an error alongside the raw, unprocessed response text, for
// a response that was received but rejected — regardless of which protocol produced it.
type RawResponseError struct {
	Err         error
	RawResponse string
}

func (e *RawResponseError) Error() string { return e.Err.Error() }
func (e *RawResponseError) Unwrap() error { return e.Err }

// ValidationError reports a pre-flight input-validation failure — invalid arguments
// rejected before anything was sent over the network.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

// parseXmlResponse parses the XML UDP response into a flat map of attribute name → value.
// XMLFields holds the parsed attributes from an XML UDP response, preserving the
// order the server sent them in (trans-id and ip first, then each requested
// feature code's fields).
type XMLFields struct {
	order       []string
	values      map[string]string
	rawResponse string
}

// Get returns the value for key, or "" if key wasn't present in the response.
func (f *XMLFields) Get(key string) string {
	return f.values[key]
}

// Keys returns every field name in the order the server sent them.
func (f *XMLFields) Keys() []string {
	return f.order
}

// RawResponse returns the full, unparsed XML response string received from the server.
func (f *XMLFields) RawResponse() string {
	return f.rawResponse
}

func parseXmlResponse(xmlResponse string) (*XMLFields, error) {
	decoder := xml.NewDecoder(strings.NewReader(xmlResponse))
	token, err := decoder.Token()
	if token == nil {
		return nil, errors.New("unexpected EOF while parsing XML response")
	}
	if err != nil {
		return nil, err
	}
	fields := &XMLFields{values: make(map[string]string), rawResponse: xmlResponse}
	switch responseElement := token.(type) {
	case xml.StartElement:
		for _, attribute := range responseElement.Attr {
			fields.order = append(fields.order, attribute.Name.Local)
			fields.values[attribute.Name.Local] = attribute.Value
		}
	default:
		return nil, errors.New("unexpected token while parsing XML response")
	}
	return fields, nil
}

// generateTransactionID returns a random alphanumeric string of the given length.
//
// Uses crypto/rand rather than math/rand: a predictable transaction ID would let an
// attacker forge a UDP response that passes the transaction-id echo check in QueryXML.
func generateTransactionID(length int) string {
	const characters = "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	randomBytes := make([]byte, length)
	if _, err := rand.Read(randomBytes); err != nil {
		panic("netacuity: crypto/rand unavailable: " + err.Error())
	}
	result := make([]byte, length)
	for i, b := range randomBytes {
		result[i] = characters[int(b)%len(characters)]
	}
	return string(result)
}

// checkAPIID reports whether apiID is in the valid range [0, 127].
func checkAPIID(apiID int) bool {
	return apiID >= 0 && apiID <= 127
}
