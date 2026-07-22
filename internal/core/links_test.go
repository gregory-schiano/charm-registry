package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMergeLinks(t *testing.T) {
	t.Parallel()

	t.Run("all fields populated", func(t *testing.T) {
		existing := map[string][]string{
			"issues": {"https://github.com/example/issues"},
		}
		got := MergeLinks(existing, "https://docs.example.com", "https://bugs.example.com", "https://src.example.com", []string{"https://web1.example.com", "https://web2.example.com"})
		assert.Equal(t, []string{"https://docs.example.com"}, got["docs"])
		assert.Equal(t, []string{"https://github.com/example/issues", "https://bugs.example.com"}, got["issues"])
		assert.Equal(t, []string{"https://src.example.com"}, got["source"])
		assert.ElementsMatch(t, []string{"https://web1.example.com", "https://web2.example.com"}, got["website"])
	})

	t.Run("empty inputs produce empty map", func(t *testing.T) {
		got := MergeLinks(nil, "", "", "", nil)
		assert.Empty(t, got)
	})

	t.Run("nil existing preserves fields", func(t *testing.T) {
		got := MergeLinks(nil, "d", "", "", nil)
		assert.Equal(t, []string{"d"}, got["docs"])
		_, hasIssues := got["issues"]
		assert.False(t, hasIssues)
	})

	t.Run("does not mutate existing", func(t *testing.T) {
		existing := map[string][]string{"docs": {"https://a.com"}}
		got := MergeLinks(existing, "https://b.com", "", "", nil)
		assert.Equal(t, []string{"https://a.com", "https://b.com"}, got["docs"])
		assert.Equal(t, []string{"https://a.com"}, existing["docs"], "existing must not be modified")
	})

	t.Run("deduplicates against existing", func(t *testing.T) {
		existing := map[string][]string{"docs": {"https://a.com"}}
		got := MergeLinks(existing, "https://a.com", "", "", nil)
		assert.Equal(t, []string{"https://a.com"}, got["docs"])
	})

	t.Run("accepts []string and StringList values", func(t *testing.T) {
		got := MergeLinks(nil,
			[]string{"https://docs1", "https://docs2"},
			StringList{"https://issues1", "https://issues1", "https://issues2"},
			StringList{"https://src"},
			nil,
		)
		assert.Equal(t, []string{"https://docs1", "https://docs2"}, got["docs"])
		assert.Equal(t, []string{"https://issues1", "https://issues2"}, got["issues"], "duplicates within a StringList are collapsed")
		assert.Equal(t, []string{"https://src"}, got["source"])
	})
}

func TestLinkValues(t *testing.T) {
	t.Parallel()

	assert.Nil(t, LinkValues(""))
	assert.Equal(t, []string{"x"}, LinkValues("x"))
	assert.Equal(t, []string{"a", "b"}, LinkValues([]string{"a", "b"}))
	assert.Equal(t, []string{"a", "b"}, LinkValues(StringList{"a", "b"}))
	assert.Nil(t, LinkValues(42), "unsupported types yield nil")
}

func TestUniqueAppend(t *testing.T) {
	t.Parallel()

	got := UniqueAppend([]string{"a", "b"}, "b")
	assert.Equal(t, []string{"a", "b"}, got, "duplicate should not be appended")

	got = UniqueAppend([]string{"a"}, "b")
	assert.Equal(t, []string{"a", "b"}, got, "new value should be appended")

	got = UniqueAppend(nil, "x")
	assert.Equal(t, []string{"x"}, got, "nil slice should work")
}
