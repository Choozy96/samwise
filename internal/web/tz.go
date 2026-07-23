package web

import (
	"fmt"
	"sort"
	"time"
)

// tzOption is one entry in the timezone datalists: an IANA name labeled with
// its CURRENT UTC offset (DST-aware, so the label matches what the zone is
// observing right now).
type tzOption struct {
	Name  string
	Label string // e.g. "UTC+08:00"
}

// tzCatalog holds representative zones covering every UTC offset from -12 to
// +14, including the half/quarter-hour zones (India, Nepal, Newfoundland, …).
// Etc/GMT+12 is the only -12 zone (its POSIX name has an inverted sign; the
// label shows the real offset).
var tzCatalog = []string{
	"Etc/GMT+12",             // -12
	"Pacific/Pago_Pago",      // -11
	"Pacific/Honolulu",       // -10
	"Pacific/Marquesas",      // -9:30
	"America/Anchorage",      // -9/-8 DST
	"America/Los_Angeles",    // -8/-7 DST
	"America/Denver",         // -7/-6 DST
	"America/Chicago",        // -6/-5 DST
	"America/New_York",       // -5/-4 DST
	"America/Caracas",        // -4
	"America/Halifax",        // -4/-3 DST
	"America/St_Johns",       // -3:30
	"America/Sao_Paulo",      // -3
	"Atlantic/South_Georgia", // -2
	"Atlantic/Azores",        // -1
	"UTC",                    // 0
	"Europe/London",          // 0/+1 DST
	"Europe/Paris",           // +1/+2 DST
	"Europe/Berlin",          // +1/+2 DST
	"Africa/Cairo",           // +2
	"Europe/Athens",          // +2/+3 DST
	"Europe/Moscow",          // +3
	"Africa/Nairobi",         // +3
	"Asia/Tehran",            // +3:30
	"Asia/Dubai",             // +4
	"Asia/Kabul",             // +4:30
	"Asia/Karachi",           // +5
	"Asia/Kolkata",           // +5:30
	"Asia/Kathmandu",         // +5:45
	"Asia/Dhaka",             // +6
	"Asia/Yangon",            // +6:30
	"Asia/Bangkok",           // +7
	"Asia/Singapore",         // +8
	"Asia/Hong_Kong",         // +8
	"Asia/Tokyo",             // +9
	"Asia/Seoul",             // +9
	"Australia/Darwin",       // +9:30
	"Australia/Brisbane",     // +10
	"Australia/Sydney",       // +10/+11 DST
	"Pacific/Guadalcanal",    // +11
	"Pacific/Auckland",       // +12/+13 DST
	"Pacific/Apia",           // +13
	"Pacific/Kiritimati",     // +14
}

// tzOptions builds the timezone datalist entries, labeled with each zone's
// current UTC offset and sorted west → east.
func tzOptions() []tzOption {
	now := time.Now()
	type row struct {
		opt tzOption
		off int
	}
	rows := make([]row, 0, len(tzCatalog))
	for _, name := range tzCatalog {
		loc, err := time.LoadLocation(name)
		if err != nil {
			continue // zone missing from the host's tzdata: skip, keep the rest
		}
		_, off := now.In(loc).Zone()
		sign, o := "+", off
		if o < 0 {
			sign, o = "-", -o
		}
		rows = append(rows, row{
			opt: tzOption{Name: name, Label: fmt.Sprintf("UTC%s%02d:%02d", sign, o/3600, (o%3600)/60)},
			off: off,
		})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].off != rows[j].off {
			return rows[i].off < rows[j].off
		}
		return rows[i].opt.Name < rows[j].opt.Name
	})
	out := make([]tzOption, len(rows))
	for i, r := range rows {
		out[i] = r.opt
	}
	return out
}
