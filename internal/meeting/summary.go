package meeting

import (
	"errors"
	"strings"
)

func ValidateSummary(s Summary, segments []Segment) error {
	ids := map[string]bool{}
	for _, s := range segments {
		ids[s.ID] = true
	}
	if strings.TrimSpace(s.Overview) == "" || len(s.Overview) > 100000 || len(s.Tasks) > 1000 || len(s.Decisions) > 1000 {
		return errors.New("Некорректное summary")
	}
	for _, items := range [][]Item{s.Decisions, s.Tasks} {
		for _, item := range items {
			if strings.TrimSpace(item.Text) == "" || len(item.Sources) == 0 || len(item.Sources) > 1000 {
				return errors.New("Summary не содержит основания решения или задачи")
			}
			for _, id := range item.Sources {
				if !ids[id] {
					return errors.New("Summary ссылается на неизвестную реплику")
				}
			}
		}
	}
	return nil
}
