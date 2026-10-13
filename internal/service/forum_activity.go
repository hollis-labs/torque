package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/hollis-labs/torque/internal/tetherclient"
)

// TorqueForumNamespace is an installation scope, never a per-board choice.
const TorqueForumNamespace = "app/torque/forum/torque"

type ForumLastPost struct {
	ID         string    `json:"id"`
	TopicID    string    `json:"topic_id"`
	CreatedAt  time.Time `json:"created_at"`
	Provenance struct {
		ID   string `json:"id"`
		Kind string `json:"kind"`
	} `json:"provenance"`
}
type ForumActivity struct {
	PostCount int            `json:"post_count"`
	LastPost  *ForumLastPost `json:"last_post"`
}

type forumActivityPage[T any] struct {
	Items      []T    `json:"items"`
	HasMore    bool   `json:"has_more"`
	NextCursor string `json:"next_cursor"`
}

// The current Tether API has no board aggregates. Read only compact metadata;
// sequences/activity timestamps also change on edits and cannot identify the
// last authored message. Walk every cursor page and compare creation times.
func (s *ForumsService) Activity(ctx context.Context, id string) (ForumActivity, error) {
	var result ForumActivity
	data, _, err := s.Do(ctx, http.MethodGet, "/forums/"+url.PathEscape(id), nil)
	if err != nil {
		return result, err
	}
	var board struct {
		Forum struct {
			Namespace string `json:"namespace"`
		} `json:"forum"`
	}
	if err := json.Unmarshal(data, &board); err != nil {
		return result, err
	}
	if board.Forum.Namespace != TorqueForumNamespace {
		return result, &tetherclient.ForumsError{Status: 404, Code: "not_found", Message: "Forum not found"}
	}
	type topic struct {
		ID        string `json:"id"`
		PostCount int    `json:"post_count"`
	}
	err = forumActivityPages(ctx, s, "/forums/"+url.PathEscape(id)+"/topics", "id,post_count", func(t topic) error {
		result.PostCount += t.PostCount
		if t.PostCount == 0 {
			return nil
		}
		return forumActivityPages(ctx, s, "/forums/topics/"+url.PathEscape(t.ID)+"/posts", "id,topic_id,created_at,provenance", func(p ForumLastPost) error {
			if result.LastPost == nil || p.CreatedAt.After(result.LastPost.CreatedAt) || (p.CreatedAt.Equal(result.LastPost.CreatedAt) && p.ID > result.LastPost.ID) {
				result.LastPost = &p
			}
			return nil
		})
	})
	return result, err
}

func forumActivityPages[T any](ctx context.Context, s *ForumsService, path, fields string, visit func(T) error) error {
	cursor := ""
	seen := map[string]bool{}
	for {
		query := url.Values{"limit": {"100"}, "fields": {fields}}
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		data, _, err := s.Do(ctx, http.MethodGet, path+"?"+query.Encode(), nil)
		if err != nil {
			return err
		}
		var page forumActivityPage[T]
		if err := json.Unmarshal(data, &page); err != nil {
			return err
		}
		for _, item := range page.Items {
			if err := visit(item); err != nil {
				return err
			}
		}
		if !page.HasMore {
			return nil
		}
		if page.NextCursor == "" || seen[page.NextCursor] {
			return fmt.Errorf("invalid Forums pagination cursor")
		}
		cursor = page.NextCursor
		seen[cursor] = true
	}
}
