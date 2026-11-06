package model

// SearchType is the kind of source a unified search hit comes from.
type SearchType string

const (
	SearchIssue    SearchType = "issue"
	SearchComment  SearchType = "comment"
	SearchDocument SearchType = "document"
)

// SearchTypes lists every source of the unified search.
var SearchTypes = []SearchType{SearchIssue, SearchComment, SearchDocument}

// ParseSearchType returns the search type named v.
func ParseSearchType(v string) (SearchType, bool) { return parseEnum(v, SearchTypes) }

// SearchHit is one result of the unified search over issues, comments and
// record documents.
//
//   - Key is the issue key for an issue or a comment and the document name for
//     a document. CommentID is set only for a comment.
//   - Title is the issue title, the comment summary or the document title; it is
//     nil when the source has none.
//   - Kind is the issue type, the comment kind or the document kind; it is nil
//     for a comment without a kind.
//   - At is when the source was last written: the issue's updated_at, the
//     comment's time, or the document's front matter date ("yyyy-mm-dd HH:MM")
//     and failing that its created_at.
//   - Snippet is the matching part with the matched terms in [ ].
type SearchHit struct {
	Type      SearchType `json:"type"`
	Project   string     `json:"project"`
	Key       string     `json:"key"`
	CommentID *int64     `json:"comment_id"`
	Title     *string    `json:"title"`
	Kind      *string    `json:"kind"`
	At        string     `json:"at"`
	Snippet   string     `json:"snippet"`
}
