package main

import (
	"context"
	"database/sql"
	"github.com/davecgh/go-spew/spew"
	"log/slog"
	"net/http"
	"slices"
	"time"
)

// chairIDを入れる
var matchingChannel chan string
var matchingInit chan struct{}

func matching() {
	ctx := context.Background()
	matchingChannel = make(chan string, 1000)
	defer close(matchingChannel)
	matchingInit = make(chan struct{})

	chairIDs := []string{}
	if err := db.SelectContext(ctx, &chairIDs, `SELECT chairs.id FROM chairs LEFT JOIN rides ON chairs.id = rides.chair_id AND rides.evaluation IS NULL WHERE is_active = TRUE AND rides.id IS NULL`); err != nil {
		slog.Error("failed to get chair ids", "error", err)
		return
	}
	for _, chairID := range chairIDs {
		matchingChannel <- chairID
	}

	slog.Info("matching start")
	defer slog.Info("matching end")
	for {
		slog.Info("matching loop")
		select {
		case <-matchingInit:
			slog.Info("matching init")
			matchingChannel = make(chan string, 1000)
		case chairID := <-matchingChannel:
			slog.Info("matching", "chair_id", chairID)
			chair := GetChair(chairID)
			if !chair.IsActive {
				continue
			}
			rides := []Ride{}
			if err := db.SelectContext(ctx, &rides, `SELECT * FROM rides WHERE chair_id IS NULL ORDER BY created_at`); err != nil {
				slog.Error("failed to get rides", "error", err)
				return
			}
			if len(rides) == 0 {
				matchingChannel <- chairID
				time.Sleep(1 * time.Second)
				slog.Info("no rides.")
				continue
			}
			chairLocation := GetChairLocation(chairID)
			if chairLocation == nil {
				spew.Dump("fail GetChairLocation, chairID: ", chairID)
				continue
			}
			ride := rides[0]
			rideIndex := 0
			// 1秒以上待っているrideがある場合は、最も待っているrideを選択
			if ride.CreatedAt.Add(1 * time.Second).Before(time.Now()) {
				for index, r := range rides {
					if abs(ride.PickupLatitude-chairLocation.Latitude)+abs(ride.PickupLongitude-chairLocation.Longitude) > abs(r.PickupLatitude-chairLocation.Latitude)+abs(r.PickupLongitude-chairLocation.Longitude) {
						ride = r
						rideIndex = index
					}
				}
			}
			slices.Delete(rides, rideIndex, rideIndex+1)
			ride.ChairID = sql.NullString{String: chairID, Valid: true}
			if _, err := db.ExecContext(ctx, "UPDATE rides SET chair_id = ? WHERE id = ? AND chair_id IS NULL", chairID, ride.ID); err != nil {
				slog.Error("failed to update ride", "error", err)
				continue
			}
			err := sendAppGetNotificationChannel(ctx, nil, "MATCHING", &ride)
			if err != nil {
				slog.Error("failed to send notification", "error", err)
				continue
			}
			err = sendChairGetNotificationChannel(ctx, "MATCHING", &ride, nil)
			if err != nil {
				slog.Error("failed to send notification", "error", err)
				continue
			}
		}
	}
}

// このAPIをインスタンス内から一定間隔で叩かせることで、椅子とライドをマッチングさせる
func internalGetMatching(w http.ResponseWriter, r *http.Request) {
	//ctx := r.Context()
	// MEMO: 一旦最も待たせているリクエストに適当な空いている椅子マッチさせる実装とする。おそらくもっといい方法があるはず…
	//tx, err := db.Beginx()
	//if err != nil {
	//	writeError(w, http.StatusInternalServerError, err)
	//	return
	//}
	//defer tx.Rollback()
	//
	//rides := []Ride{}
	//if err := tx.SelectContext(ctx, &rides, `SELECT * FROM rides WHERE chair_id IS NULL ORDER BY created_at`); err != nil {
	//	writeError(w, http.StatusInternalServerError, err)
	//	return
	//}
	//
	//notCompletedChairIDs := []string{}
	//if err := tx.SelectContext(ctx, &notCompletedChairIDs, `SELECT chair_id FROM rides where evaluation IS NULL AND chair_id IS NOT NULL`); err != nil {
	//	writeError(w, http.StatusInternalServerError, err)
	//	return
	//}
	//notCompletedChairIDsSet := make(map[string]struct{}, len(notCompletedChairIDs))
	//for _, id := range notCompletedChairIDs {
	//	notCompletedChairIDsSet[id] = struct{}{}
	//}
	//notCompletedChairIDs = []string{}
	//if err := tx.SelectContext(ctx, &notCompletedChairIDs, `SELECT chair_id FROM rides where updated_at > NOW(6) - INTERVAL 3.5 SECOND AND chair_id IS NOT NULL`); err != nil {
	//	writeError(w, http.StatusInternalServerError, err)
	//	return
	//}
	//for _, id := range notCompletedChairIDs {
	//	notCompletedChairIDsSet[id] = struct{}{}
	//}
	//
	//chairs := []Chair{}
	//if err := tx.SelectContext(ctx, &chairs, `SELECT * FROM chairs WHERE is_active = TRUE`); err != nil {
	//	writeError(w, http.StatusInternalServerError, err)
	//	return
	//}
	//candidateChairs := make([]Chair, 0, len(chairs))
	//for _, chair := range chairs {
	//	if _, ok := notCompletedChairIDsSet[chair.ID]; !ok {
	//		candidateChairs = append(candidateChairs, chair)
	//	}
	//}
	//
	//for _, ride := range rides {
	//	if len(candidateChairs) == 0 {
	//		break
	//	}
	//	selectedChair := candidateChairs[0]
	//	selectedIndex := 0
	//	for index, chair := range candidateChairs {
	//		selectedChairLocation := GetChairLocation(selectedChair.ID)
	//		candidateChairLocation := GetChairLocation(chair.ID)
	//		if abs(selectedChairLocation.Latitude-ride.PickupLatitude)+abs(selectedChairLocation.Longitude-ride.PickupLongitude) > abs(candidateChairLocation.Latitude-ride.PickupLatitude)+abs(candidateChairLocation.Longitude-ride.PickupLongitude) {
	//			selectedChair = chair
	//			selectedIndex = index
	//		}
	//	}
	//	candidateChairs = slices.Delete(candidateChairs, selectedIndex, selectedIndex+1)
	//	if _, err := tx.ExecContext(ctx, "UPDATE rides SET chair_id = ? WHERE id = ? AND chair_id IS NULL", selectedChair.ID, ride.ID); err != nil {
	//		writeError(w, http.StatusInternalServerError, err)
	//		return
	//	}
	//	err = sendAppGetNotificationChannel(ctx, tx, "MATCHING", &ride)
	//	if err != nil {
	//		writeError(w, http.StatusInternalServerError, err)
	//		slog.Error("failed to send notification", "error", err)
	//		return
	//	}
	//}
	//
	//if err := tx.Commit(); err != nil {
	//	writeError(w, http.StatusInternalServerError, err)
	//	return
	//}

	w.WriteHeader(http.StatusNoContent)
}
