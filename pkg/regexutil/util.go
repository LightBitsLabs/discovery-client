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
