package main

import (
	"net/http"
)

// このAPIをインスタンス内から一定間隔で叩かせることで、椅子とライドをマッチングさせる
func internalGetMatching(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	// MEMO: 一旦最も待たせているリクエストに適当な空いている椅子マッチさせる実装とする。おそらくもっといい方法があるはず…
	rides := []Ride{}
	if err := db.SelectContext(ctx, &rides, `SELECT * FROM rides WHERE chair_id IS NULL ORDER BY created_at`); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	notCompletedChairIDs := []string{}
	if err := db.SelectContext(ctx, &notCompletedChairIDs, `SELECT chair_id FROM rides where evaluation IS NULL`); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	notCompletedChairIDsSet := make(map[string]struct{}, len(notCompletedChairIDs))
	for _, id := range notCompletedChairIDs {
		notCompletedChairIDsSet[id] = struct{}{}
	}

	chairs := []Chair{}
	if err := db.SelectContext(ctx, &chairs, `SELECT * FROM chairs WHERE is_active = TRUE`); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	candidateChairIDs := []string{}
	for _, chair := range chairs {
		if _, ok := notCompletedChairIDsSet[chair.ID]; !ok {
			candidateChairIDs = append(candidateChairIDs, chair.ID)
		}
	}

	for id, ride := range rides {
		if len(candidateChairIDs) == id {
			break
		}
		if _, err := db.ExecContext(ctx, "UPDATE rides SET chair_id = ? WHERE id = ?", candidateChairIDs[id], ride.ID); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
	}

	w.WriteHeader(http.StatusNoContent)
}
