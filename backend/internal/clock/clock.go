package clock

import "time"

// Clock is the only source of current time for domain services.
type Clock interface{ Now() time.Time }

type System struct{}

func (System) Now() time.Time { return time.Now().UTC() }

type Region string

const (
	America Region = "america"
	Europe  Region = "europe"
	Asia    Region = "asia"
)

var offsets = map[Region]int{America: -300, Europe: 60, Asia: 540}

func Offset(region Region) (time.Duration, bool) {
	minutes, ok := offsets[region]
	return time.Duration(minutes) * time.Minute, ok
}

func FixedLocation(region Region) (*time.Location, bool) {
	offset, ok := offsets[region]
	if !ok {
		return nil, false
	}
	return time.FixedZone(string(region), offset*60), true
}

func LocalDate(instant time.Time, region Region) (time.Time, bool) {
	location, ok := FixedLocation(region)
	if !ok {
		return time.Time{}, false
	}
	local := instant.In(location)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location), true
}
