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

import "fmt"

type stackdriverUtil struct {
	metrics map[string]int64
	apiName string
	success bool
}

func newStackdriverUtil(name string) *stackdriverUtil {
	m := make(map[string]int64)
	return &stackdriverUtil{m, name, false}
}

func (util *stackdriverUtil) addMetric(key string, value int64) {
	(util.metrics)[key] = value
}

func (util *stackdriverUtil) setSuccess() {
	util.success = true
}

func (util *stackdriverUtil) addMetricsDict(metrics map[string]int64) {
	for key, value := range metrics {
		(util.metrics)[key] = value
	}
}

func (util *stackdriverUtil) outputMetrics() {
	if util.success {
		fmt.Printf("%s_success 1\n", util.apiName)
	} else {
		fmt.Printf("%s_success 0\n", util.apiName)
	}
	for key, value := range util.metrics {
		fmt.Printf("%s %d\n", key, value)
	}
}
