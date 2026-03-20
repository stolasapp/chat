package match

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/stolasapp/chat/internal/catalog"
)

func TestMatcher_BasicMatch(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		matcher := NewMatcher(DefaultMatchTimeout)
		go matcher.Run(t.Context())

		matcher.Enqueue(t.Context(), "tok-a", &Profile{
			Gender:    catalog.GenderMale,
			Role:      catalog.RoleDominant,
			Interests: catalog.NewSet(catalog.Interest("Basketball")),
		})
		matcher.Enqueue(t.Context(), "tok-b", &Profile{
			Gender:    catalog.GenderFemale,
			Role:      catalog.RoleSubmissive,
			Interests: catalog.NewSet(catalog.Interest("Basketball")),
		})

		synctest.Wait()

		select {
		case result := <-matcher.Matched():
			tokens := []Token{result.A.Token, result.B.Token}
			assert.Contains(t, tokens, Token("tok-a"))
			assert.Contains(t, tokens, Token("tok-b"))
		default:
			t.Fatal("expected match")
		}
	})
}

func TestMatcher_IncompatibleNoMatch(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		matcher := NewMatcher(DefaultMatchTimeout)
		go matcher.Run(t.Context())

		// A wants female only, B is male
		matcher.Enqueue(t.Context(), "tok-a", &Profile{
			Gender:       catalog.GenderMale,
			Role:         catalog.RoleDominant,
			FilterGender: catalog.NewSet(catalog.GenderFemale),
			Interests:    catalog.NewSet(catalog.Interest("Basketball")),
		})
		matcher.Enqueue(t.Context(), "tok-b", &Profile{
			Gender:    catalog.GenderMale,
			Role:      catalog.RoleSubmissive,
			Interests: catalog.NewSet(catalog.Interest("Basketball")),
		})

		synctest.Wait()

		select {
		case <-matcher.Matched():
			t.Fatal("expected no match")
		default:
			// good
		}
	})
}

func TestMatcher_BestPairSelection(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		matcher := NewMatcher(DefaultMatchTimeout)
		go matcher.Run(t.Context())

		// A and C share basketball; A and B share nothing
		matcher.Enqueue(t.Context(), "tok-a", &Profile{
			Gender:    catalog.GenderMale,
			Role:      catalog.RoleDominant,
			Interests: catalog.NewSet(catalog.Interest("Basketball")),
		})
		matcher.Enqueue(t.Context(), "tok-b", &Profile{
			Gender:    catalog.GenderFemale,
			Role:      catalog.RoleSubmissive,
			Interests: catalog.NewSet(catalog.Interest("Tennis")),
		})
		matcher.Enqueue(t.Context(), "tok-c", &Profile{
			Gender:    catalog.GenderFemale,
			Role:      catalog.RoleSubmissive,
			Interests: catalog.NewSet(catalog.Interest("Basketball")),
		})

		synctest.Wait()

		select {
		case result := <-matcher.Matched():
			// A and C should match (higher score)
			tokens := []Token{result.A.Token, result.B.Token}
			assert.Contains(t, tokens, Token("tok-a"))
			assert.Contains(t, tokens, Token("tok-c"))
		default:
			t.Fatal("expected match")
		}
	})
}

func TestMatcher_LeaveRemovesFromQueue(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		matcher := NewMatcher(DefaultMatchTimeout)
		go matcher.Run(t.Context())

		matcher.Enqueue(t.Context(), "tok-a", &Profile{
			Gender:    catalog.GenderMale,
			Role:      catalog.RoleDominant,
			Interests: catalog.NewSet(catalog.Interest("Basketball")),
		})

		// wait for A to be processed
		synctest.Wait()

		// remove A
		matcher.Leave(t.Context(), "tok-a")

		// wait for leave to be processed
		synctest.Wait()

		// now add B
		matcher.Enqueue(t.Context(), "tok-b", &Profile{
			Gender:    catalog.GenderFemale,
			Role:      catalog.RoleSubmissive,
			Interests: catalog.NewSet(catalog.Interest("Basketball")),
		})

		synctest.Wait()

		select {
		case <-matcher.Matched():
			t.Fatal("expected no match after leave")
		default:
			// good
		}
	})
}

func TestMatcher_TimeoutFallback(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		matcher := NewMatcher(DefaultMatchTimeout)
		go matcher.Run(t.Context())

		// add two users with disjoint interests
		matcher.Enqueue(t.Context(), "tok-a", &Profile{
			Gender:    catalog.GenderMale,
			Role:      catalog.RoleDominant,
			Interests: catalog.NewSet(catalog.Interest("Basketball")),
		})
		matcher.Enqueue(t.Context(), "tok-b", &Profile{
			Gender:    catalog.GenderFemale,
			Role:      catalog.RoleSubmissive,
			Interests: catalog.NewSet(catalog.Interest("Tennis")),
		})

		synctest.Wait()

		// no match yet (score 0, not timed out)
		select {
		case <-matcher.Matched():
			t.Fatal("should not match before timeout")
		default:
		}

		// advance past timeout; the ticker will fire and rerun
		// matchPass
		time.Sleep(DefaultMatchTimeout + matchTickInterval)

		synctest.Wait()

		select {
		case result := <-matcher.Matched():
			assert.NotNil(t, result.A)
			assert.NotNil(t, result.B)
		default:
			t.Fatal("expected timeout fallback match")
		}
	})
}

func TestMatcher_BlockedPairSkipped(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		matcher := NewMatcher(DefaultMatchTimeout)
		go matcher.Run(t.Context())

		matcher.Enqueue(t.Context(), "tok-a", &Profile{
			Gender:        catalog.GenderMale,
			Role:          catalog.RoleDominant,
			Interests:     catalog.NewSet(catalog.Interest("Basketball")),
			BlockedTokens: catalog.NewSet[Token]("tok-b"),
		})
		matcher.Enqueue(t.Context(), "tok-b", &Profile{
			Gender:    catalog.GenderFemale,
			Role:      catalog.RoleSubmissive,
			Interests: catalog.NewSet(catalog.Interest("Basketball")),
		})

		synctest.Wait()

		select {
		case <-matcher.Matched():
			t.Fatal("expected no match for blocked pair")
		default:
			// good
		}
	})
}
