/*
 *
 * Copyright 2019 gRPC authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */

package main

import (
	"log"
	"os"

	spanner "cloud.google.com/go/spanner/apiv1"
)

func parseArgs() []bool {
	// Currently we only have spanner probers. May add new features in the future.
	vars := []bool{false}
	for _, arg := range os.Args {
		if arg == "--spanner" {
			vars[0] = true
		}
	}
	return vars
}

type spannerProber func(*spanner.Client, map[string]int64) error

func executeSpannerProber(p spannerProber, client *spanner.Client, metrics map[string]int64, count *int) {
	err := p(client, metrics)
	if err != nil {
		*count = (*count) + 1
		log.Fatal(err.Error())
	}
}

func executeSpannerProbers() {
	metrics := make(map[string]int64)
	client := createClient()
	failureCount := 0
	executeSpannerProber(sessionManagementProber, client, metrics, &failureCount)
	executeSpannerProber(executeSqlProber, client, metrics, &failureCount)
	executeSpannerProber(readProber, client, metrics, &failureCount)
	executeSpannerProber(transactionProber, client, metrics, &failureCount)
	executeSpannerProber(partitionProber, client, metrics, &failureCount)

	util := newStackdriverUtil("Spanner")
	if failureCount == 0 {
		util.setSuccess()
	}
	util.addMetricsDict(metrics)
	util.outputMetrics()
}

func main() {
	log.Print("Start probing...")
	vars := parseArgs()
	if vars[0] {
		executeSpannerProbers()
	}
}
