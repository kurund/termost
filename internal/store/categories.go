package store

import (
	"sort"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

type Category struct {
	ID        string
	Name      string
	Type      model.SidebarCategoryType
	Collapsed bool
	Channels  []string
}

// SetCategories stores a team's sidebar categories in the user's order.
func (s *Server) SetCategories(teamID string, ordered *model.OrderedSidebarCategories) {
	if ordered == nil {
		delete(s.categories, teamID)
		return
	}
	byID := map[string]*model.SidebarCategoryWithChannels{}
	for _, c := range ordered.Categories {
		byID[c.Id] = c
	}
	var cats []*model.SidebarCategoryWithChannels
	for _, id := range ordered.Order {
		if c, ok := byID[id]; ok {
			cats = append(cats, c)
		}
	}
	s.categories[teamID] = cats
}

// TeamCategories returns the team's non-DM categories with known channels,
// sorted as the official clients sort them. Without category data the team's
// channels form a single "Channels" category.
func (s *Server) TeamCategories(teamID string) []Category {
	cats, ok := s.categories[teamID]
	if !ok {
		return []Category{{ID: teamID + "/channels", Name: "Channels", Type: model.SidebarCategoryChannels, Channels: s.teamChannels[teamID]}}
	}
	var out []Category
	for _, c := range cats {
		if c.Type == model.SidebarCategoryDirectMessages {
			continue
		}
		var ids []string
		for _, id := range c.Channels {
			if _, known := s.Channels[id]; known {
				ids = append(ids, id)
			}
		}
		out = append(out, Category{
			ID:        c.Id,
			Name:      c.DisplayName,
			Type:      c.Type,
			Collapsed: c.Collapsed,
			Channels:  s.sortCategory(ids, c.Sorting),
		})
	}
	return out
}

// CategorisedDMs reports DMs placed in a team category (e.g. Favorites), which
// the official clients show there instead of under Direct Messages.
func (s *Server) CategorisedDMs() map[string]bool {
	out := map[string]bool{}
	for teamID := range s.categories {
		for _, c := range s.TeamCategories(teamID) {
			for _, id := range c.Channels {
				if s.Channels[id].IsGroupOrDirect() {
					out[id] = true
				}
			}
		}
	}
	return out
}

func (s *Server) sortCategory(ids []string, sorting model.SidebarCategorySorting) []string {
	switch sorting {
	case model.SidebarCategorySortManual:
		return ids
	case model.SidebarCategorySortRecent:
		sort.SliceStable(ids, func(i, j int) bool {
			return s.Channels[ids[i]].LastPostAt > s.Channels[ids[j]].LastPostAt
		})
	default:
		sort.SliceStable(ids, func(i, j int) bool {
			return strings.ToLower(s.ChannelName(ids[i])) < strings.ToLower(s.ChannelName(ids[j]))
		})
	}
	return ids
}
