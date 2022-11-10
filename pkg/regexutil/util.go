// Copyright 2016--2022 Lightbits Labs Ltd.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// you may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package regexutil

import "regexp"

type ParamsMap map[string]string
type RepeatedParamsMap map[int]ParamsMap

func GetRepeatedParams(pattern *regexp.Regexp, input string) RepeatedParamsMap {
	matches := pattern.FindAllStringSubmatch(input, -1)

	paramsMap := make(RepeatedParamsMap)
	for submatchIndex, match := range matches {
		if paramsMap[submatchIndex] == nil {
			paramsMap[submatchIndex] = make(ParamsMap)
		}
		for i, name := range pattern.SubexpNames() {
			if i > 0 && i <= len(match) {
				paramsMap[submatchIndex][name] = match[i]
			}
		}
	}
	return paramsMap
}

/**
 * Parses url with the given regular expression and returns the
 * group values defined in the expression.
 *
 */
func GetParams(pattern *regexp.Regexp, input string) ParamsMap {
	match := pattern.FindStringSubmatch(input)

	paramsMap := make(map[string]string)
	for i, name := range pattern.SubexpNames() {
		if i > 0 && i <= len(match) {
			paramsMap[name] = match[i]
		}
	}
	return paramsMap
}
