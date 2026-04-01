package view

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/templui/templui/utils"
)

func TestTwMerge_ConcurrentSafe(t *testing.T) {
	t.Parallel()

	var waitGroup sync.WaitGroup
	for range 100 {
		waitGroup.Go(func() {
			result := utils.TwMerge("bg-red-500", "bg-blue-500")
			assert.Equal(t, "bg-blue-500", result)
		})
	}
	waitGroup.Wait()
}
