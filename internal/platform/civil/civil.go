// Package civil provides calendar types without a time zone: Date (a business
// date, stay night, date of birth) and TimeOfDay (check-in policy time).
//
// Business dates must never be carried as time.Time: a time.Time is an instant,
// and converting it between zones silently moves the calendar day. civil.Date
// cannot be shifted by a time zone, maps to PostgreSQL `date`, and serialises
// as "YYYY-MM-DD".
package civil

import (
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

const dateLayout = "2006-01-02"

// Date is a calendar date. The zero value is "no date" (IsZero).
type Date struct {
	t time.Time // always midnight UTC
}

// NewDate returns the date y-m-d (normalised like time.Date).
func NewDate(y int, m time.Month, d int) Date {
	return Date{time.Date(y, m, d, 0, 0, 0, 0, time.UTC)}
}

// ParseDate parses "YYYY-MM-DD" strictly.
func ParseDate(s string) (Date, error) {
	t, err := time.Parse(dateLayout, s)
	if err != nil {
		return Date{}, fmt.Errorf("civil: invalid date %q (want YYYY-MM-DD)", s)
	}
	return Date{t}, nil
}

// MustParseDate is ParseDate for constants and tests.
func MustParseDate(s string) Date {
	d, err := ParseDate(s)
	if err != nil {
		panic(err)
	}
	return d
}

// DateOf returns the calendar date of instant t as seen in t's location.
// Callers must convert to the property time zone first: DateOf(now.In(loc)).
func DateOf(t time.Time) Date { return NewDate(t.Year(), t.Month(), t.Day()) }

func (d Date) IsZero() bool          { return d.t.IsZero() }
func (d Date) Year() int             { return d.t.Year() }
func (d Date) Month() time.Month     { return d.t.Month() }
func (d Date) Day() int              { return d.t.Day() }
func (d Date) Weekday() time.Weekday { return d.t.Weekday() }
func (d Date) AddDays(n int) Date    { return Date{d.t.AddDate(0, 0, n)} }
func (d Date) Before(o Date) bool    { return d.t.Before(o.t) }
func (d Date) After(o Date) bool     { return d.t.After(o.t) }
func (d Date) Equal(o Date) bool     { return d.t.Equal(o.t) }
func (d Date) Compare(o Date) int    { return d.t.Compare(o.t) }
func (d Date) DaysUntil(o Date) int  { return int(o.t.Sub(d.t).Hours() / 24) }
func (d Date) String() string        { return d.t.Format(dateLayout) }
func (d Date) GoString() string      { return "civil.MustParseDate(\"" + d.String() + "\")" }
func (d Date) MarshalText() ([]byte, error) {
	if d.IsZero() {
		return nil, fmt.Errorf("civil: cannot marshal a zero Date (use *civil.Date for optional dates)")
	}
	return []byte(d.String()), nil
}

func (d *Date) UnmarshalText(b []byte) error {
	v, err := ParseDate(string(b))
	if err != nil {
		return err
	}
	*d = v
	return nil
}

// At returns the instant at which this date reaches time-of-day tod in loc.
func (d Date) At(tod TimeOfDay, loc *time.Location) time.Time {
	return time.Date(d.Year(), d.Month(), d.Day(), tod.Hour(), tod.Minute(), 0, 0, loc)
}

// ScanDate implements pgtype.DateScanner (reading PostgreSQL `date`).
// NULL is rejected: use *civil.Date for nullable columns.
func (d *Date) ScanDate(v pgtype.Date) error {
	if !v.Valid {
		return fmt.Errorf("civil: cannot scan NULL into civil.Date (use *civil.Date)")
	}
	if v.InfinityModifier != pgtype.Finite {
		return fmt.Errorf("civil: infinite dates are not supported")
	}
	*d = DateOf(v.Time)
	return nil
}

// DateValue implements pgtype.DateValuer (writing PostgreSQL `date`). A zero
// Date is written as NULL so NOT NULL columns reject it.
func (d Date) DateValue() (pgtype.Date, error) {
	if d.IsZero() {
		return pgtype.Date{}, nil
	}
	return pgtype.Date{Time: d.t, Valid: true}, nil
}

// TimeOfDay is a wall-clock time with minute precision (e.g. check-in at 14:00).
type TimeOfDay struct {
	minutes int // minutes since midnight, 0..1439
}

// NewTimeOfDay returns hh:mm, or an error if out of range.
func NewTimeOfDay(hour, minute int) (TimeOfDay, error) {
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return TimeOfDay{}, fmt.Errorf("civil: invalid time of day %02d:%02d", hour, minute)
	}
	return TimeOfDay{hour*60 + minute}, nil
}

// ParseTimeOfDay parses "HH:MM" (24-hour).
func ParseTimeOfDay(s string) (TimeOfDay, error) {
	t, err := time.Parse("15:04", s)
	if err != nil || len(s) != 5 {
		return TimeOfDay{}, fmt.Errorf("civil: invalid time of day %q (want HH:MM)", s)
	}
	return TimeOfDay{t.Hour()*60 + t.Minute()}, nil
}

// MustParseTimeOfDay is ParseTimeOfDay for constants and tests.
func MustParseTimeOfDay(s string) TimeOfDay {
	t, err := ParseTimeOfDay(s)
	if err != nil {
		panic(err)
	}
	return t
}

// TimeOfDayOf returns the wall-clock time of t in t's location.
func TimeOfDayOf(t time.Time) TimeOfDay { return TimeOfDay{t.Hour()*60 + t.Minute()} }

func (t TimeOfDay) Hour() int                    { return t.minutes / 60 }
func (t TimeOfDay) Minute() int                  { return t.minutes % 60 }
func (t TimeOfDay) Before(o TimeOfDay) bool      { return t.minutes < o.minutes }
func (t TimeOfDay) Compare(o TimeOfDay) int      { return t.minutes - o.minutes }
func (t TimeOfDay) String() string               { return fmt.Sprintf("%02d:%02d", t.Hour(), t.Minute()) }
func (t TimeOfDay) MarshalText() ([]byte, error) { return []byte(t.String()), nil }

func (t *TimeOfDay) UnmarshalText(b []byte) error {
	v, err := ParseTimeOfDay(string(b))
	if err != nil {
		return err
	}
	*t = v
	return nil
}

// ScanTime implements pgtype.TimeScanner (reading PostgreSQL `time`).
func (t *TimeOfDay) ScanTime(v pgtype.Time) error {
	if !v.Valid {
		return fmt.Errorf("civil: cannot scan NULL into civil.TimeOfDay")
	}
	*t = TimeOfDay{int(v.Microseconds / int64(time.Minute/time.Microsecond))}
	return nil
}

// TimeValue implements pgtype.TimeValuer (writing PostgreSQL `time`).
func (t TimeOfDay) TimeValue() (pgtype.Time, error) {
	return pgtype.Time{Microseconds: int64(t.minutes) * int64(time.Minute/time.Microsecond), Valid: true}, nil
}
