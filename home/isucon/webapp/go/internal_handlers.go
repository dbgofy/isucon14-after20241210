package main

import (
	"context"
	"database/sql"
	"fmt"
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

	{
		chairIDs := []string{}
		if err := db.SelectContext(ctx, &chairIDs, `SELECT chairs.id FROM chairs LEFT JOIN rides ON chairs.id = rides.chair_id AND rides.evaluation IS NULL WHERE is_active = TRUE AND rides.id IS NULL`); err != nil {
			slog.Error("failed to get chair ids", "error", err)
			return
		}
		for _, chairID := range chairIDs {
			matchingChannel <- chairID
		}
	}

	slog.Info("matching start")
	defer slog.Info("matching end")
	for {
		slog.Info("matching loop")
		select {
		case <-matchingInit:
			slog.Info("matching init")
			matchingChannel = make(chan string, 1000)
			{
				chairIDs := []string{}
				if err := db.SelectContext(ctx, &chairIDs, `SELECT chairs.id FROM chairs LEFT JOIN rides ON chairs.id = rides.chair_id AND rides.evaluation IS NULL WHERE is_active = TRUE AND rides.id IS NULL`); err != nil {
					slog.Error("failed to get chair ids", "error", err)
					continue
				}
				for _, chairID := range chairIDs {
					matchingChannel <- chairID
				}
			}
		case chairID := <-matchingChannel:
			slog.Info("matching", "chair_id", chairID)
			chair := GetChair(chairID)
			if !chair.IsActive {
				continue
			}
			rides := []Ride{}
			if err := db.SelectContext(ctx, &rides, `SELECT * FROM rides WHERE chair_id IS NULL ORDER BY created_at`); err != nil {
				slog.Error("failed to get rides", "error", err)
				continue
			}
			if len(rides) == 0 {
				matchingChannel <- chairID
				time.Sleep(1 * time.Second)
				slog.Info("no rides.")
				continue
			}
			ride := rides[0]
			// 3秒以上待っているrideがある場合は、今あるchairを全て取り出して、うまくマッチングさせる
			if ride.CreatedAt.Add(3 * time.Second).Before(time.Now()) {
				chairIDs := make([]string, 0, len(rides))
				chairIDs = append(chairIDs, chairID)
			LOOP:
				for {
					select {
					case cID := <-matchingChannel:
						chairIDs = append(chairIDs, cID)
					default:
						break LOOP // ラベル付きのbreakを使う
					}
				}
				chairLocations := make([]*ChairLocation, 0, len(chairIDs))
				for _, cID := range chairIDs {
					l := GetChairLocation(cID)
					if l == nil {
						matchingChannel <- cID
						slog.Error("fail GetChairLocation", "chairID", cID)
						continue
					}
					chairLocations = append(chairLocations, l)
				}
				if len(chairLocations) == 0 {
					slog.Info("no chair locations")
					continue
				}
				for _, r := range rides {
					if len(chairLocations) == 0 {
						break
					}
					ride = r
					chairLocation := chairLocations[0]
					selectChairLocationIndex := 0
					for index, cl := range chairLocations {
						if abs(ride.PickupLatitude-chairLocation.Latitude)+abs(ride.PickupLongitude-chairLocation.Longitude) > abs(r.PickupLatitude-cl.Latitude)+abs(r.PickupLongitude-cl.Longitude) {
							ride = r
							chairLocation = cl
							selectChairLocationIndex = index
						}
					}

					err := matchingComp(ctx, ride, chairLocation.ChairID)
					if err != nil {
						slog.Error("failed to matching", "error", err)
						break
					}

					slices.Delete(chairLocations, selectChairLocationIndex, selectChairLocationIndex+1)
				}
				for _, cl := range chairLocations {
					matchingChannel <- cl.ChairID
				}
			} else {
				chairLocation := GetChairLocation(chairID)
				if chairLocation == nil {
					matchingChannel <- chairID
					spew.Dump("fail GetChairLocation, chairID: ", chairID)
					continue
				}
				for _, r := range rides {
					if abs(ride.PickupLatitude-chairLocation.Latitude)+abs(ride.PickupLongitude-chairLocation.Longitude) > abs(r.PickupLatitude-chairLocation.Latitude)+abs(r.PickupLongitude-chairLocation.Longitude) {
						ride = r
					}
				}
			}
			err := matchingComp(ctx, ride, chairID)
			if err != nil {
				slog.Error("failed to matching", "error", err)
				continue
			}
		}
	}
}

func matchingComp(ctx context.Context, ride Ride, chairID string) error {
	ride.ChairID = sql.NullString{String: chairID, Valid: true}
	if _, err := db.ExecContext(ctx, "UPDATE rides SET chair_id = ? WHERE id = ? AND chair_id IS NULL", chairID, ride.ID); err != nil {
		slog.Error("failed to update ride", "error", err)
		return fmt.Errorf("failed to update ride: %w", err)
	}
	err := sendAppGetNotificationChannel(ctx, nil, "MATCHING", &ride)
	if err != nil {
		slog.Error("failed to send notification", "error", err)
		return fmt.Errorf("failed to send notification: %w", err)
	}
	err = sendChairGetNotificationChannel(ctx, "MATCHING", &ride, nil)
	if err != nil {
		slog.Error("failed to send notification", "error", err)
		return fmt.Errorf("failed to send notification: %w", err)
	}
	return nil
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
