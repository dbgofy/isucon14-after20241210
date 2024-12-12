package main

import (
	"net/http"
	"slices"
)

// このAPIをインスタンス内から一定間隔で叩かせることで、椅子とライドをマッチングさせる
func internalGetMatching(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	// MEMO: 一旦最も待たせているリクエストに適当な空いている椅子マッチさせる実装とする。おそらくもっといい方法があるはず…
	tx, err := db.Beginx()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer tx.Rollback()

	rides := []Ride{}
	if err := tx.SelectContext(ctx, &rides, `SELECT * FROM rides WHERE chair_id IS NULL ORDER BY created_at`); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	notCompletedChairIDs := []string{}
	if err := tx.SelectContext(ctx, &notCompletedChairIDs, `SELECT chair_id FROM rides where evaluation IS NULL AND chair_id IS NOT NULL`); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	notCompletedChairIDsSet := make(map[string]struct{}, len(notCompletedChairIDs))
	for _, id := range notCompletedChairIDs {
		notCompletedChairIDsSet[id] = struct{}{}
	}
	notCompletedChairIDs = []string{}
	if err := tx.SelectContext(ctx, &notCompletedChairIDs, `SELECT chair_id FROM rides where updated_at > NOW(6) - INTERVAL 3.5 SECOND AND chair_id IS NOT NULL`); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	for _, id := range notCompletedChairIDs {
		notCompletedChairIDsSet[id] = struct{}{}
	}

	chairs := []Chair{}
	if err := tx.SelectContext(ctx, &chairs, `SELECT * FROM chairs WHERE is_active = TRUE`); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	candidateChairs := make([]Chair, 0, len(chairs))
	for _, chair := range chairs {
		if _, ok := notCompletedChairIDsSet[chair.ID]; !ok {
			candidateChairs = append(candidateChairs, chair)
		}
	}

	for _, ride := range rides {
		if len(candidateChairs) == 0 {
			break
		}
		selectedChair := candidateChairs[0]
		selectedIndex := 0
		for index, chair := range candidateChairs {
			selectedChairLocation := GetChairLocation(selectedChair.ID)
			candidateChairLocation := GetChairLocation(chair.ID)
			if abs(selectedChairLocation.Latitude-ride.PickupLatitude)+abs(selectedChairLocation.Longitude-ride.PickupLongitude) > abs(candidateChairLocation.Latitude-ride.PickupLatitude)+abs(candidateChairLocation.Longitude-ride.PickupLongitude) {
				selectedChair = chair
				selectedIndex = index
			}
		}
		candidateChairs = slices.Delete(candidateChairs, selectedIndex, selectedIndex+1)
		if _, err := tx.ExecContext(ctx, "UPDATE rides SET chair_id = ? WHERE id = ? AND chair_id IS NULL", selectedChair.ID, ride.ID); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		err = sendAppGetNotificationChannel(ctx, tx, "MATCHING", &ride)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
	}

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
