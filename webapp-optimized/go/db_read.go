package isureal

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
)

// Every interpolation here is a fixed SQL fragment, never request text.
func interactionJSON(comments bool, condition string, limit int) string {
	table, alias, owner, fn, direction := "realmojis", "r", "reactor_id", "opt_reaction", "DESC"
	live := "r.replaced_at IS NULL AND r.deleted_at IS NULL"
	kind := "reaction"
	if comments {
		table, alias, owner, fn, direction = "comments", "c", "author_id", "opt_comment", "ASC"
		live = "c.deleted_at IS NULL"
		kind = "comment"
	}
	return fmt.Sprintf(`COALESCE((SELECT jsonb_agg(record ORDER BY created_at %s,id %s) FROM (
  SELECT %s.created_at,%s.id,jsonb_build_object('%s',%s(%s),'user',opt_user(u)) AS record
  FROM %s %s JOIN users u ON u.id=%s.%s AND u.status='active'
  WHERE %s AND %s ORDER BY %s.created_at %s,%s.id %s LIMIT %d
 ) records),'[]'::jsonb)`, direction, direction, alias, alias, kind, fn, alias,
		table, alias, alias, owner, live, condition, alias, direction, alias, direction, limit)
}

var commentsPageSQL = `WITH permission AS MATERIALIZED (SELECT opt_access($1,$2,true) AS allowed)
 SELECT allowed,CASE WHEN allowed THEN ` + interactionJSON(true,
	`c.post_id=$2 AND ($3::timestamptz IS NULL OR (c.created_at,c.id)>($3,$4))`, 51) + ` ELSE '[]'::jsonb END AS items FROM permission`
var reactionsPageSQL = `WITH permission AS MATERIALIZED (SELECT opt_access($1,$2,false) AS allowed)
 SELECT allowed,CASE WHEN allowed THEN ` + interactionJSON(false,
	`r.post_id=$2 AND ($3::timestamptz IS NULL OR (r.created_at,r.id)<($3,$4))`, 51) + ` ELSE '[]'::jsonb END AS items FROM permission`

func decodeRecords(data []byte) ([]writeResult, error) {
	var items []writeResult
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}
	for i := range items {
		if err := items[i].prepareUser(); err != nil {
			return nil, err
		}
	}
	return items, nil
}

func readInteractions(ctx context.Context, db sqlx.QueryerContext, actor, post string, comments bool, at *time.Time, id string) ([]writeResult, bool, error) {
	// The hot state holds the comments and RealMojis of the posts of open
	// windows and decides access to them.
	if records, allowed, ok := hot.interactions(realActor(ctx, actor), post, comments, at, id); ok {
		return records, allowed, nil
	}
	query := reactionsPageSQL
	if comments {
		query = commentsPageSQL
	}
	var page struct {
		Allowed bool   `db:"allowed"`
		Items   []byte `db:"items"`
	}
	if err := sqlx.GetContext(ctx, db, &page, query, actor, post, at, id); err != nil {
		return nil, false, err
	}
	records, err := decodeRecords(page.Items)
	return records, page.Allowed, err
}

var memoriesSQL = `WITH page AS MATERIALIZED (
 SELECT p AS post,w AS win,u AS owner,p.posted_at,p.id,
   row_number() OVER(ORDER BY p.posted_at DESC,p.id DESC) AS ordinal
 FROM posts p JOIN posting_windows w ON w.id=p.posting_window_id
 JOIN users u ON u.id=p.author_id AND u.status='active'
 WHERE p.deleted_at IS NULL AND p.author_id=$1 AND w.local_date >= $2 AND w.local_date < $3
   AND ($4::timestamptz IS NULL OR (p.posted_at,p.id)<($4,$5))
 ORDER BY p.posted_at DESC,p.id DESC LIMIT 51
) SELECT jsonb_build_object('post',opt_post(page.post,page.win),'user',opt_user(page.owner)) AS item,
 CASE WHEN ordinal<=50 THEN ` + interactionJSON(true, `c.post_id=page.id`, 50) + ` ELSE '[]'::jsonb END AS comments,
 CASE WHEN ordinal<=50 THEN ` + interactionJSON(false, `r.post_id=page.id`, 50) + ` ELSE '[]'::jsonb END AS reactions
 FROM page ORDER BY posted_at DESC,id DESC`
