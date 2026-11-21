package diskgrowth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type Preferences struct {
	Enabled bool   `json:"enabled"`
	At      string `json:"at"`
	Days    int    `json:"days"`
}

func (p Preferences) Validate() error {
	_, err := time.Parse("15:04", p.At)
	if err != nil || len(p.At) != 5 || p.Days < 7 || p.Days > 15 {
		return errors.New("请选择有效的每日时间和 7–15 天的保留期")
	}
	return nil
}

type History struct{ Dir string }

func (h History) Preferences() (Preferences, error) {
	p := Preferences{At: "09:00", Days: 14}
	b, err := os.ReadFile(filepath.Join(h.Dir, "preferences.json"))
	if os.IsNotExist(err) {
		return p, nil
	}
	if err == nil {
		err = json.Unmarshal(b, &p)
	}
	if err == nil {
		err = p.Validate()
	}
	return p, err
}
func (h History) Save(p Preferences) error {
	if err := p.Validate(); err != nil {
		return err
	}
	return h.write("preferences.json", p)
}
func (h History) write(name string, value any) error {
	if err := os.MkdirAll(h.Dir, 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(h.Dir, ".saving-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	err = json.NewEncoder(tmp).Encode(value)
	closeErr := tmp.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(tmp.Name(), filepath.Join(h.Dir, name))
}

const stamp = "20060102T150405.000000000Z"

type Record struct {
	ID      string    `json:"id"`
	Started time.Time `json:"started"`
	Trigger string    `json:"trigger"`
}
type Entry struct {
	Sample
	Error string `json:"error,omitempty"`
}

func (h History) Write(s Entry) error {
	return h.write(s.Started.UTC().Format(stamp)+"_"+s.Trigger+".json", s)
}
func (h History) Read(r Record) (Entry, error) {
	var s Entry
	b, err := os.ReadFile(filepath.Join(h.Dir, r.ID))
	if err == nil {
		err = json.Unmarshal(b, &s)
	}
	return s, err
}
func (h History) Records(now time.Time, days int) ([]Record, error) {
	entries, err := os.ReadDir(h.Dir)
	if os.IsNotExist(err) {
		return []Record{}, nil
	}
	if err != nil {
		return nil, err
	}
	cutoff := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, 1-days)
	records := []Record{}
	for _, entry := range entries {
		name := entry.Name()
		ts, trigger, ok := strings.Cut(strings.TrimSuffix(name, ".json"), "_")
		if !ok || !strings.HasSuffix(name, ".json") || !slices.Contains([]string{"manual", "daily", "catchup"}, trigger) {
			continue
		}
		t, e := time.Parse(stamp, ts)
		if e != nil {
			continue
		}
		if t.Before(cutoff) {
			if err := os.Remove(filepath.Join(h.Dir, name)); err != nil && !os.IsNotExist(err) {
				return nil, err
			}
			continue
		}
		records = append(records, Record{name, t, trigger})
	}
	slices.Reverse(records)
	return records, nil
}

// Daily runs once per local calendar day after the scheduled time, including after wake or restart.
func Due(now time.Time, p Preferences, records []Record) (trigger, planned string) {
	if !p.Enabled {
		return "", ""
	}
	day := now.Format(time.DateOnly)
	planned = day + " " + p.At
	if now.Format("15:04") < p.At {
		return "", ""
	}
	for _, r := range records {
		if r.Trigger != "manual" && r.Started.In(now.Location()).Format(time.DateOnly) == day {
			return "", ""
		}
	}
	if now.Format("15:04") == p.At {
		return "daily", planned
	}
	return "catchup", planned
}
func (h History) Pair(records []Record, id string) (*Entry, *Entry, error) {
	start := 0
	if id != "" {
		start = slices.IndexFunc(records, func(r Record) bool { return r.ID == id })
		if start < 0 {
			return nil, nil, fmt.Errorf("记录已过期，请重新选择")
		}
	}
	var current *Entry
	for _, r := range records[start:] {
		s, e := h.Read(r)
		if e != nil {
			return nil, nil, e
		}
		if current == nil {
			current = &s
			if s.Error != "" {
				break
			}
			continue
		}
		if s.Error == "" && current.Root == s.Root {
			return current, &s, nil
		}
	}
	return current, nil, nil
}
