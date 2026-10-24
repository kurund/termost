package store

import "github.com/mattermost/mattermost/server/public/model"

// ApplyReaction adds or removes r on the matching post in posts and reports
// whether one was found.
func ApplyReaction(posts []*model.Post, r *model.Reaction, added bool) bool {
	for _, p := range posts {
		if p.Id != r.PostId {
			continue
		}
		if p.Metadata == nil {
			p.Metadata = &model.PostMetadata{}
		}
		kept := p.Metadata.Reactions[:0]
		exists := false
		for _, existing := range p.Metadata.Reactions {
			same := existing.UserId == r.UserId && existing.EmojiName == r.EmojiName
			exists = exists || same
			if !same || added {
				kept = append(kept, existing)
			}
		}
		p.Metadata.Reactions = kept
		if added && !exists {
			p.Metadata.Reactions = append(p.Metadata.Reactions, r)
		}
		return true
	}
	return false
}

func (s *Server) ApplyReaction(channelID string, r *model.Reaction, added bool) {
	if p, ok := s.Posts[channelID]; ok {
		ApplyReaction(p.List, r, added)
	}
}

type ReactionSummary struct {
	Emoji string
	Count int
	Mine  bool
	Users []string
}

// Reactions groups a post's reactions by emoji in order of first use.
func (s *Server) Reactions(p *model.Post) []ReactionSummary {
	if p.Metadata == nil {
		return nil
	}
	var out []ReactionSummary
	index := map[string]int{}
	for _, r := range p.Metadata.Reactions {
		i, ok := index[r.EmojiName]
		if !ok {
			i = len(out)
			index[r.EmojiName] = i
			out = append(out, ReactionSummary{Emoji: r.EmojiName})
		}
		out[i].Count++
		out[i].Mine = out[i].Mine || r.UserId == s.Me.Id
		out[i].Users = append(out[i].Users, r.UserId)
	}
	return out
}

// HasReacted reports whether the current user already reacted with emoji.
func (s *Server) HasReacted(p *model.Post, emoji string) bool {
	if p.Metadata == nil {
		return false
	}
	for _, r := range p.Metadata.Reactions {
		if r.UserId == s.Me.Id && r.EmojiName == emoji {
			return true
		}
	}
	return false
}
