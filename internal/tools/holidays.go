package tools

import "time"

// USFederalHoliday returns the name of the US federal holiday on the given
// date (in the date's own location), or "" if the date is not a holiday.
// It checks both the date's own observance (e.g. Memorial Day) and the
// observed-on-weekday rule for fixed-date holidays that fall on a weekend
// (e.g. Jul 4 on Saturday is observed Friday Jul 3).
func USFederalHoliday(d time.Time) string {
	y, m, day := d.Year(), d.Month(), d.Day()
	wd := d.Weekday()

	// Fixed-date holidays — exact match.
	switch {
	case m == time.January && day == 1:
		return "New Year's Day"
	case m == time.June && day == 19:
		return "Juneteenth National Independence Day"
	case m == time.July && day == 4:
		return "Independence Day"
	case m == time.November && day == 11:
		return "Veterans Day"
	case m == time.December && day == 25:
		return "Christmas Day"
	}

	// Floating holidays — nth weekday of month.
	if m == time.January && wd == time.Monday && nthWeekdayOfMonth(d) == 3 {
		return "Martin Luther King Jr. Day"
	}
	if m == time.February && wd == time.Monday && nthWeekdayOfMonth(d) == 3 {
		return "Presidents' Day"
	}
	if m == time.May && wd == time.Monday && isLastWeekdayOfMonth(d) {
		return "Memorial Day"
	}
	if m == time.September && wd == time.Monday && nthWeekdayOfMonth(d) == 1 {
		return "Labor Day"
	}
	if m == time.October && wd == time.Monday && nthWeekdayOfMonth(d) == 2 {
		return "Columbus Day"
	}
	if m == time.November && wd == time.Thursday && nthWeekdayOfMonth(d) == 4 {
		return "Thanksgiving Day"
	}

	// Observed-on-weekday rules for fixed-date holidays:
	// - If the holiday falls on Saturday, it's observed the preceding Friday.
	// - If on Sunday, observed the following Monday.
	if wd == time.Friday {
		next := d.AddDate(0, 0, 1) // Saturday
		if name := fixedDateHoliday(next.Year(), next.Month(), next.Day()); name != "" {
			return name + " (observed)"
		}
	}
	if wd == time.Monday {
		prev := d.AddDate(0, 0, -1) // Sunday
		if name := fixedDateHoliday(prev.Year(), prev.Month(), prev.Day()); name != "" {
			return name + " (observed)"
		}
	}

	_ = y
	return ""
}

func fixedDateHoliday(_ int, m time.Month, day int) string {
	switch {
	case m == time.January && day == 1:
		return "New Year's Day"
	case m == time.June && day == 19:
		return "Juneteenth National Independence Day"
	case m == time.July && day == 4:
		return "Independence Day"
	case m == time.November && day == 11:
		return "Veterans Day"
	case m == time.December && day == 25:
		return "Christmas Day"
	}
	return ""
}

// nthWeekdayOfMonth returns the ordinal (1..5) of d's weekday within its month
// (e.g. 1 for the first Monday, 3 for the third Thursday).
func nthWeekdayOfMonth(d time.Time) int {
	return (d.Day()-1)/7 + 1
}

// isLastWeekdayOfMonth reports whether d is the final occurrence of its
// weekday in its month (used for "last Monday of May" = Memorial Day).
func isLastWeekdayOfMonth(d time.Time) bool {
	next := d.AddDate(0, 0, 7)
	return next.Month() != d.Month()
}
