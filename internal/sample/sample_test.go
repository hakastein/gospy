package sample

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestCollection(t *testing.T) {
	t.Run("AddSamples", func(t *testing.T) {
		t.Run("IncrementsCountsCorrectly", func(t *testing.T) {
			collection := newCollection(100)

			collection.AddSample("funcA;funcB;funcC", "tag1=value1")
			collection.AddSample("funcA;funcB;funcC", "tag1=value1")
			collection.AddSample("funcD;funcE", "tag2=value2")

			assert.Equal(t, 2, collection.Len(), "Collection should have 2 unique samples")

			tagSamples := collection.Samples()

			t.Run("tag1=value1", func(t *testing.T) {
				t.Parallel()
				samplesTag1, exists1 := tagSamples["tag1=value1"]
				assert.True(t, exists1, "tag1=value1 should exist in samples")
				assert.Len(t, samplesTag1, 1, "tag1=value1 should have 1 sample")
				assert.Equal(t, 2, samplesTag1[sampleHash("funcA;funcB;funcC", "tag1=value1")].count, "Sample count for tag1=value1 should be 2")
			})

			t.Run("tag2=value2", func(t *testing.T) {
				t.Parallel()
				samplesTag2, exists2 := tagSamples["tag2=value2"]
				assert.True(t, exists2, "tag2=value2 should exist in samples")
				assert.Len(t, samplesTag2, 1, "tag2=value2 should have 1 sample")
				assert.Equal(t, 1, samplesTag2[sampleHash("funcD;funcE", "tag2=value2")].count, "Sample count for tag2=value2 should be 1")
			})
		})
	})

	t.Run("Props", func(t *testing.T) {
		t.Parallel()
		collection := newCollection(100)
		// Simulate some duration between from and until
		time.Sleep(1 * time.Second)
		collection.Finish()

		from, until, rateHz := collection.Props()

		assert.NotZero(t, from, "From timestamp should not be zero")
		assert.NotZero(t, until, "Until timestamp should not be zero")
		assert.True(t, until >= from, "Until timestamp should be greater than or equal to from timestamp")
		assert.Equal(t, 100, rateHz, "RateHz should be 100")
	})
}

func TestFoldedStacksToCollection(t *testing.T) {
	t.Run("AggregatesSamplesProperly", func(t *testing.T) {
		t.Parallel()
		collectionChannel := make(chan *Collection, 1)
		foldedStacksChannel := make(chan [2]string, 3)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go FoldedStacksToCollection(ctx, foldedStacksChannel, collectionChannel, 100*time.Millisecond, 100)

		foldedStacksChannel <- [2]string{"funcA;funcB;funcC", "tag1=value1"}
		foldedStacksChannel <- [2]string{"funcA;funcB;funcC", "tag1=value1"}
		foldedStacksChannel <- [2]string{"funcD;funcE", "tag2=value2"}

		time.Sleep(150 * time.Millisecond) // Wait for the accumulation interval to pass

		close(foldedStacksChannel)

		collection := <-collectionChannel
		assert.NotNil(t, collection, "Collection should not be nil")
		assert.Equal(t, 2, collection.Len(), "Collection should have 2 unique samples")
	})
}
