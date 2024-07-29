package helpers

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/cxmcc/unixsums/cksum"
)

func ConvertCrontab(seedstring, cron string) (string, error) {
	// Seed is used to generate pseudo random numbers.
	// The seed is based on the seedstring, so will not change
	seed := cksum.Cksum([]byte(fmt.Sprintf("%s\n", seedstring)))
	var minutes, hours, days, months, dayweek string
	splitCron := strings.Split(cron, " ")
	// check the provided cron splits into 5
	if len(splitCron) == 5 {
		for idx, val := range splitCron {
			if idx == 0 {
				match1, _ := regexp.MatchString("^(M|H)$", val)
				if match1 {
					// If just an `M` or `H` (for backwards compatibility) is defined, we
					// generate a pseudo random minute.
					minutes = strconv.Itoa(int(math.Mod(float64(seed), 60)))
					continue
				}
				match2, _ := regexp.MatchString("^(M|H|\\*)/([0-5]?[0-9])$", val)
				if match2 {
					// A Minute like M/15 (or H/15 or */15 for backwards compatibility) is defined, create a list of minutes with a random start
					// like 4,19,34,49 or 6,21,36,51
					params := getCaptureBlocks("^(?P<P1>M|H|\\*)/(?P<P2>[0-5]?[0-9])$", val)
					step, err := strconv.Atoi(params["P2"])
					if err != nil {
						return "", fmt.Errorf("cron definition '%s' is invalid, unable to determine minutes value", cron)
					}
					counter := int(math.Mod(float64(seed), float64(step)))
					var minutesArr []string
					for counter < 60 {
						minutesArr = append(minutesArr, fmt.Sprintf("%d", counter))
						counter += step
					}
					minutes = strings.Join(minutesArr, ",")
					continue
				}
				if isInCSVRange(val, 0, 59) {
					// A minute like 0,10,15,30,59
					minutes = val
					continue
				}
				if isInRange(val, 0, 59) {
					// A minute like 0-59
					minutes = val
					continue
				}
				if val == "*" {
					// otherwise pass the * through
					minutes = val
					continue
				}
				// if the value is not valid, return an error with where the issue is
				if minutes == "" {
					return "", fmt.Errorf("cron definition '%s' is invalid, unable to determine minutes value", cron)
				}
			}
			if idx == 1 {
				match1, _ := regexp.MatchString("^H$", val)
				if match1 {
					// If just an `H` is defined, we generate a pseudo random hour.
					hours = strconv.Itoa(int(math.Mod(float64(seed), 24)))
					continue
				}
				match2, _ := regexp.MatchString("^H\\(([01]?[0-9]|2[0-3])-([01]?[0-9]|2[0-3])\\)$", val)
				if match2 {
					// If H is defined with a given range, example: H(2-4), we generate a random hour between 2-4
					params := getCaptureBlocks("^H\\((?P<P1>[01]?[0-9]|2[0-3])-(?P<P2>[01]?[0-9]|2[0-3])\\)$", val)
					hFrom, err := strconv.Atoi(params["P1"])
					if err != nil {
						return "", fmt.Errorf("cron definition '%s' is invalid, unable to determine hours value", cron)
					}
					hTo, err := strconv.Atoi(params["P2"])
					if err != nil {
						return "", fmt.Errorf("cron definition '%s' is invalid, unable to determine hours value", cron)
					}
					if hFrom < hTo {
						// Example: HOUR_FROM: 2, HOUR_TO: 4
						// Calculate the difference between the two hours (in example will be 2)
						maxDiff := float64(hTo - hFrom)
						// Generate a difference based on the SEED (in example will be 0, 1 or 2)
						diff := int(math.Mod(float64(seed), maxDiff))
						// Add the generated difference to the FROM hour (in example will be 2, 3 or 4)
						hours = strconv.Itoa(hFrom + diff)
						continue
					}
					if hFrom > hTo {
						// If the FROM is larger than the TO, we have a range like 22-2
						// Calculate the difference between the two hours with a 24 hour jump (in example will be 4)
						maxDiff := float64(24 - hFrom + hTo)
						// Generate a difference based on the SEED (in example will be 0, 1, 2, 3 or 4)
						diff := int(math.Mod(float64(seed), maxDiff))
						// Add the generated difference to the FROM hour (in example will be 22, 23, 24, 25 or 26)
						if hFrom+diff >= 24 {
							// If the hour is higher than 24, we subtract 24 to handle the midnight change
							hours = strconv.Itoa(hFrom + diff - 24)
							continue
						}
						hours = strconv.Itoa(hFrom + diff)
						continue
					}
					if hFrom == hTo {
						hours = strconv.Itoa(hFrom)
						continue
					}
				}
				if isInCSVRange(val, 0, 23) {
					hours = val
					continue
				}
				if isInRange(val, 0, 23) {
					hours = val
					continue
				}
				if val == "*" {
					hours = val
					continue
				}
				// if the value is not valid, return an error with where the issue is
				if hours == "" {
					return "", fmt.Errorf("cron definition '%s' is invalid, unable to determine hours value", cron)
				}
			}
			if idx == 2 {
				match1, _ := regexp.MatchString("^D$", val)
				if match1 {
					// If just an `D` is defined, we generate a pseudo random day of the month, but only generated up to 31
					// so february is never skipped
					days = strconv.Itoa(int(math.Mod(float64(seed), 32)))
					// days can't be 0, support 1-31 only
					if days == "0" {
						days = "1"
					}
					continue
				}
				match2, _ := regexp.MatchString("^D\\(([01]?[0-9]|2[0-9]|3[0-1])-([01]?[0-9]|2[0-9]|3[0-1])\\)$", val)
				if match2 {
					// If D is defined with a given range, example: D(2-4), we generate a random day of the month between 2-4
					params := getCaptureBlocks("^D\\((?P<P1>[01]?[0-9]|2[0-9]|3[0-1])-(?P<P2>[01]?[0-9]|2[0-9]|3[0-1])\\)$", val)
					hFrom, err := strconv.Atoi(params["P1"])
					if err != nil {
						return "", fmt.Errorf("cron definition '%s' is invalid, unable to determine day of month value", cron)
					}
					if hFrom == 0 {
						return "", fmt.Errorf("cron definition '%s' is invalid, unable to determine day of month value, starting day can't be 0", cron)
					}
					hTo, err := strconv.Atoi(params["P2"])
					if err != nil {
						return "", fmt.Errorf("cron definition '%s' is invalid, unable to determine day of month value", cron)
					}
					if hFrom < hTo {
						maxDiff := float64(hTo - hFrom)
						diff := int(math.Mod(float64(seed), maxDiff))
						days = strconv.Itoa(hFrom + diff)
						continue
					}
					if hFrom > hTo {
						maxDiff := float64(29 - hFrom + hTo)
						diff := int(math.Mod(float64(seed), maxDiff))
						if hFrom+diff >= 29 {
							days = strconv.Itoa(hFrom + diff - 29)
							continue
						}
						days = strconv.Itoa(hFrom + diff)
						continue
					}
					if hFrom == hTo {
						days = strconv.Itoa(hFrom)
						continue
					}
				}
				if isInCSVRange(val, 1, 31) {
					days = val
					continue
				}
				if isInRange(val, 1, 31) {
					days = val
					continue
				}
				if val == "*" {
					days = val
					continue
				}
				// if the value is not valid, return an error with where the issue is
				if days == "" {
					return "", fmt.Errorf("cron definition '%s' is invalid, unable to determine days value", cron)
				}
			}
			if idx == 3 {
				if isInCSVRange(val, 1, 12) {
					months = val
					continue
				}
				if isInRange(val, 1, 12) {
					months = val
					continue
				}
				if val == "*" {
					months = val
					continue
				}
				// if the value is not valid, return an error with where the issue is
				if months == "" {
					return "", fmt.Errorf("cron definition '%s' is invalid, unable to determine months value", cron)
				}
			}
			if idx == 4 {
				match1, _ := regexp.MatchString("^D$", val)
				if match1 {
					// If just an `D` is defined, we generate a pseudo random day of the week.
					dayweek = strconv.Itoa(int(math.Mod(float64(seed), 6)))
					continue
				}
				match2, _ := regexp.MatchString("^D\\(([0-6])-([0-6])\\)$", val)
				if match2 {
					// If D is defined with a given range, example: D(2-4), we generate a random day of the week between 2-4
					params := getCaptureBlocks("^D\\((?P<P1>[0-6])-(?P<P2>[0-6])\\)$", val)
					hFrom, err := strconv.Atoi(params["P1"])
					if err != nil {
						return "", fmt.Errorf("cron definition '%s' is invalid, unable to determine day value", cron)
					}
					hTo, err := strconv.Atoi(params["P2"])
					if err != nil {
						return "", fmt.Errorf("cron definition '%s' is invalid, unable to determine day value", cron)
					}
					if hFrom < hTo {
						maxDiff := float64(hTo - hFrom)
						diff := int(math.Mod(float64(seed), maxDiff))
						dayweek = strconv.Itoa(hFrom + diff)
						continue
					}
					if hFrom > hTo {
						maxDiff := float64(6 - hFrom + hTo)
						diff := int(math.Mod(float64(seed), maxDiff))
						if hFrom+diff >= 6 {
							dayweek = strconv.Itoa(hFrom + diff - 6)
							continue
						}
						dayweek = strconv.Itoa(hFrom + diff)
						continue
					}
					if hFrom == hTo {
						dayweek = strconv.Itoa(hFrom)
						continue
					}
				}
				if isInCSVRange(val, 0, 6) {
					dayweek = val
					continue
				}
				if isInRange(val, 0, 6) {
					dayweek = val
					continue
				}
				if val == "*" {
					dayweek = val
					continue
				}
				// if the value is not valid, return an error with where the issue is
				if dayweek == "" {
					return "", fmt.Errorf("cron definition '%s' is invalid, unable to determine day(week) value", cron)
				}
			}
		}
		return fmt.Sprintf("%v %v %v %v %v", minutes, hours, days, months, dayweek), nil
	}
	return "", fmt.Errorf("cron definition '%s' is invalid", cron)
}

func getCaptureBlocks(regex, val string) (captureMap map[string]string) {
	var regexComp = regexp.MustCompile(regex)
	match := regexComp.FindStringSubmatch(val)
	captureMap = make(map[string]string)
	for i, name := range regexComp.SubexpNames() {
		if i > 0 && i <= len(match) {
			captureMap[name] = match[i]
		}
	}
	return captureMap
}

// check if the provided cron time definition is a valid `1,2,4,8` type range
func isInCSVRange(s string, min, max int) bool {
	items := strings.Split(s, ",")
	for _, val := range items {
		num, err := strconv.Atoi(val)
		if err != nil {
			// not a number, return false
			return false
		}
		if num < min || num > max {
			// outside range, return false
			return false
		}
	}
	return true
}

// check if the provided cron time definition is a valid `1-2` type range
func isInRange(s string, min, max int) bool {
	items := strings.Split(s, "-")
	if len(items) > 2 || len(items) < 1 {
		// too  many or not enough items split by -
		return false
	}
	hFrom, err := strconv.Atoi(items[0])
	if err != nil {
		// not a number or error checking if it is, return false
		return false
	}
	hTo, err := strconv.Atoi(items[1])
	if err != nil {
		// not a number or error checking if it is, return false
		return false
	}
	if hFrom > hTo || hFrom < min || hFrom > max || hTo < min || hTo > max {
		// numbers in range are not in valid format of LOW-HIGH
		return false
	}
	return true
}
