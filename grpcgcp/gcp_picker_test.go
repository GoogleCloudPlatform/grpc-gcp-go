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

package grpcgcp
 
import (
	"fmt"
	"testing"
)

type TestMsg struct {
	Key string
	NestedField *NestedField
}

type NestedField struct {
	Key string
}

func (nested *NestedField) getNestedKey() string {
	return nested.Key
}

func TestGetKeyFromMessage(t *testing.T) {
	expectedRes := "test_key"
	msg := &TestMsg{
		Key: expectedRes,
		NestedField: &NestedField{
			Key: "test_nested_key",
		},
	}
	locator := "key"
	res, err := getAffinityKeyFromMessage(locator, msg)
	if err != nil {
		t.Fatalf("getAffinityKeyFromMessage failed: %v", err)
	}
	if res != expectedRes {
		t.Fatalf("getAffinityKeyFromMessage returns wrong key: %v, want: %v", res, expectedRes)
	}
}

func TestGetNestedKeyFromMessage(t *testing.T) {
	expectedRes := "test_nested_key"
	msg := &TestMsg{
		Key: "test_key",
		NestedField: &NestedField{
			Key: expectedRes,
		},
	}
	locator := "nestedField.key"
	res, err := getAffinityKeyFromMessage(locator, msg)
	if err != nil {
		t.Fatalf("getAffinityKeyFromMessage failed: %v", err)
	}
	if res != expectedRes {
		t.Fatalf("getAffinityKeyFromMessage returns wrong key: %v, want: %v", res, expectedRes)
	}
}

func TestInvalidKeyLocator(t *testing.T) {
	msg := &TestMsg{
		Key: "test_key",
		NestedField: &NestedField{
			Key: "test_nested_key",
		},
	}

	locator := "invalidLocator"
	expectedErr := fmt.Sprintf("cannot get valid affinity key from locator: %v", locator)
	_, err := getAffinityKeyFromMessage(locator, msg)
	if err == nil || err.Error() != expectedErr {
		t.Fatalf("getAffinityKeyFromMessage returns wrong err: %v, want: %v", err, expectedErr)
	}

	locator = "key.invalidLocator"
	expectedErr = fmt.Sprintf("cannot get valid affinity key from locator: %v", locator)
	_, err = getAffinityKeyFromMessage(locator, msg)
	if err == nil || err.Error() != expectedErr {
		t.Fatalf("getAffinityKeyFromMessage returns wrong err: %v, want: %v", err, expectedErr)
	}

	locator = "nestedField.key.invalidLocator"
	expectedErr = fmt.Sprintf("cannot get valid affinity key from locator: %v", locator)
	_, err = getAffinityKeyFromMessage(locator, msg)
	if err == nil || err.Error() != expectedErr {
		t.Fatalf("getAffinityKeyFromMessage returns wrong err: %v, want: %v", err, expectedErr)
	}
}