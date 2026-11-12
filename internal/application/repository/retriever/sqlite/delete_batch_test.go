package sqlite

import (
	"context"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A converted manual produces more embeddings than fit in one statement.
// Deleting them must clear the metadata, the vector table and the keyword index
// in batches — a per-row loop held SQLite's only write lock for minutes on a
// 13k-chunk document, which stalled the whole application.
func TestDeleteByKnowledgeIDListClearsMoreThanOneBatch(t *testing.T) {
	repository := newSQLiteRetrieverTestRepository(t)
	const total = deleteBatchSize*2 + 25

	infos := make([]*types.IndexInfo, 0, total)
	embeddings := make(map[string][]float32, total)
	for i := 0; i < total; i++ {
		chunkID := fmt.Sprintf("chunk-%04d", i)
		info := sqliteTestIndex(chunkID, "kb", "manual", "", true)
		info.Content = "shared-token " + chunkID
		infos = append(infos, info)
		embeddings[info.SourceID] = []float32{1, 0}
	}
	require.NoError(t, repository.BatchSave(context.Background(), infos, map[string]any{
		"embedding": embeddings,
	}))

	var before int64
	require.NoError(t, repository.db.Model(&sqliteEmbedding{}).Count(&before).Error)
	require.Equal(t, int64(total), before)

	require.NoError(t, repository.DeleteByKnowledgeIDList(
		context.Background(), []string{"manual"}, 2, ""))

	var after int64
	require.NoError(t, repository.db.Model(&sqliteEmbedding{}).Count(&after).Error)
	assert.Zero(t, after, "every metadata row must be removed")

	var vectors int64
	require.NoError(t, repository.db.Raw("SELECT count(*) FROM vec_embeddings_2").Scan(&vectors).Error)
	assert.Zero(t, vectors, "every vector row must be removed")

	if repository.ftsUnavailable {
		t.Log("FTS5 unavailable (build without sqlite_fts5); skipping keyword index check")
		return
	}
	var keywords int64
	require.NoError(t, repository.db.Raw("SELECT count(*) FROM lite_embeddings_fts").Scan(&keywords).Error)
	assert.Zero(t, keywords, "every keyword index row must be removed")
}
