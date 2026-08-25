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

// Command xml demonstrates a multi-feature XML UDP query against a NetAcuity server.
//
// Usage:
//
//	go run ./examples/xml/main.go <server-ip> <query-ip> <feature-code>[,<feature-code>...]
package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	netacuity "github.com/netacuity/netacuity-client-api-go"
)

func main() {
	if len(os.Args) != 4 {
		fmt.Println("Usage: <server-ip> <query-ip> <comma-delimited feature-codes>")
		os.Exit(1)
	}

	var exampleAPIID int = 81
	var timeout time.Duration = 3 * time.Second
	var serverIP net.IP = net.ParseIP(os.Args[1])
	var queryIP net.IP = net.ParseIP(os.Args[2])

	fcTokens := strings.Split(os.Args[3], ",")
	featureCodes := make([]int, len(fcTokens))
	for i, fc := range fcTokens {
		featureCode, err := strconv.ParseInt(fc, 10, 0)
		if err != nil {
			fmt.Printf("Error: %v\n", err.Error())
			os.Exit(1)
		}
		featureCodes[i] = int(featureCode)
	}

	client, err := netacuity.NewClient(serverIP, exampleAPIID, timeout)
	if err != nil {
		fmt.Printf("Error: %v\n", err.Error())
		os.Exit(1)
	}

	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000_000))
	if err != nil {
		fmt.Printf("Error: %v\n", err.Error())
		os.Exit(1)
	}
	transactionID := n.String()

	result, err := client.QueryXMLWithTransactionID(context.Background(), queryIP, featureCodes, transactionID)
	if err != nil {
		fmt.Printf("Error: %v\n", err.Error())
		os.Exit(1)
	}

	fmt.Printf("ip = %v\n", result.Get("ip"))
	fmt.Printf("trans-id = %v\n", result.Get("trans-id"))
	for _, field := range result.Keys() {
		if field == "ip" || field == "trans-id" {
			continue
		}
		fmt.Printf("%s = %v\n", field, result.Get(field))
	}
	fmt.Printf("raw-response = %v\n", result.RawResponse())
}
