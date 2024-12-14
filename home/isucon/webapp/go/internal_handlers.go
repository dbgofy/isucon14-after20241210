package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"
)

// chairIDを入れる
var matchingChairChannel chan string

// rideを入れる
var matchingRideChannel chan Ride
var matchingInit chan struct{}

func matching() {
	ctx := context.Background()
	matchingChairChannel = make(chan string, 1000)
	defer close(matchingChairChannel)
	matchingRideChannel = make(chan Ride, 1000)
	defer close(matchingRideChannel)
	matchingInit = make(chan struct{})

	{
		chairIDs := []string{}
		if err := db.SelectContext(ctx, &chairIDs, `SELECT chairs.id FROM chairs LEFT JOIN rides ON chairs.id = rides.chair_id AND rides.evaluation IS NULL WHERE is_active = TRUE AND rides.id IS NULL`); err != nil {
			slog.Error("failed to get chair ids", "error", err)
			return
		}
		for _, chairID := range chairIDs {
			matchingChairChannel <- chairID
		}
	}

	chairModelByChairName := make(map[string]ChairModel)
	{
		chairModels := []ChairModel{}
		if err := db.SelectContext(ctx, &chairModels, "SELECT * FROM chair_models"); err != nil {
			slog.Error("failed to get chair models", "error", err)
			return
		}
		for _, chairModel := range chairModels {
			chairModelByChairName[chairModel.Name] = chairModel
		}
	}

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	waitingChairIDs := []string{}
	waitingRides := []Ride{}
	slog.Info("matching start")
	defer slog.Info("matching end")
	for {
		slog.Info("matching loop")
		select {
		case <-matchingInit:
			slog.Info("matching init")
			matchingChairChannel = make(chan string, 1000)
			matchingRideChannel = make(chan Ride, 1000)
			{
				waitingChairIDs = []string{}
				if err := db.SelectContext(ctx, &waitingChairIDs, `SELECT chairs.id FROM chairs LEFT JOIN rides ON chairs.id = rides.chair_id AND rides.evaluation IS NULL WHERE is_active = TRUE AND rides.id IS NULL`); err != nil {
					slog.Error("failed to get chair ids", "error", err)
					continue
				}
				waitingRides = []Ride{}
				if err := db.SelectContext(ctx, &waitingRides, `SELECT * FROM rides WHERE chair_id IS NULL ORDER BY created_at`); err != nil {
					slog.Error("failed to get rides", "error", err)
					continue
				}
			}
		case chairID := <-matchingChairChannel:
			waitingChairIDs = append(waitingChairIDs, chairID)
		case ride := <-matchingRideChannel:
			waitingRides = append(waitingRides, ride)
		case <-ticker.C:
			type expectedScoreType struct {
				ride          Ride
				chairLocation *ChairLocation
				expectedScore float64
			}
			expectedScores := make([]expectedScoreType, 0, len(waitingChairIDs)*len(waitingRides))
			for _, chairID := range waitingChairIDs {
				chairLocation := GetChairLocation(chairID)
				if chairLocation == nil {
					continue
				}
				chair := GetChair(chairID)
				if chair == nil {
					continue
				}
				for _, r := range waitingRides {
					expectedScores = append(expectedScores, expectedScoreType{
						ride:          r,
						chairLocation: chairLocation,
						expectedScore: calcExpectedScore(r, chairLocation, chairModelByChairName[chair.Model].Speed) +
							float64((time.Now().Sub(r.CreatedAt)).Nanoseconds())*0.000000001, // うまく待ってる時間を考慮したい
					})
				}
			}
			if len(expectedScores) == 0 {
				continue
			}
			sort.Slice(expectedScores, func(i, j int) bool {
				return expectedScores[i].expectedScore > expectedScores[j].expectedScore
			})
			//highExpectedScore := expectedScores[0].expectedScore
			usedRideIDs := make(map[string]struct{})
			usedChairIDs := make(map[string]struct{})
			matchingRides := make([]Ride, 0, len(expectedScores))
			for _, es := range expectedScores {
				//if es.expectedScore < highExpectedScore*0.1 { // 10%以下のものは無視
				//	break
				//}
				if _, ok := usedRideIDs[es.ride.ID]; ok {
					continue
				}
				if _, ok := usedChairIDs[es.chairLocation.ChairID]; ok {
					continue
				}
				es.ride.ChairID = sql.NullString{String: es.chairLocation.ChairID, Valid: true}
				matchingRides = append(matchingRides, es.ride)

				usedRideIDs[es.ride.ID] = struct{}{}
				usedChairIDs[es.chairLocation.ChairID] = struct{}{}
			}

			err := matchingComp(ctx, matchingRides)
			if err != nil {
				slog.Error("failed to matching", "error", err)
				continue
			}

			waitingChairIDs = slices.DeleteFunc(waitingChairIDs, func(chairID string) bool {
				_, ok := usedChairIDs[chairID]
				return ok
			})
			waitingRides = slices.DeleteFunc(waitingRides, func(ride Ride) bool {
				_, ok := usedRideIDs[ride.ID]
				return ok
			})
		}
	}
}

func calcExpectedScore(ride Ride, nowChairLocation *ChairLocation, speed int) float64 {
	var ret float64
	// 椅子がライドとマッチした位置から乗車位置までの移動距離の合計 * 0.1
	distanceOfChairToPickup := float64(calculateDistance(nowChairLocation.Latitude, nowChairLocation.Longitude, ride.PickupLatitude, ride.PickupLongitude))
	ret += (distanceOfChairToPickup) * 0.1
	// 椅子の乗車位置から目的地までの移動距離の合計
	distanceOfPickupToDestination := float64(calculateDistance(ride.PickupLatitude, ride.PickupLongitude, ride.DestinationLatitude, ride.DestinationLongitude))
	ret += distanceOfPickupToDestination

	// かかる時間
	t := (distanceOfChairToPickup + distanceOfPickupToDestination) / float64(speed)

	// 単位時間あたりの得点の期待値
	return ret / t
}

func matchingComp(ctx context.Context, rides []Ride) error {
	rideIDs := make([]string, 0, len(rides))
	chairIDs := make([]string, 0, len(rides))
	for _, ride := range rides {
		rideIDs = append(rideIDs, ride.ID)
		chairIDs = append(chairIDs, ride.ChairID.String)
	}
	a := append(append(rideIDs, chairIDs...), rideIDs...)
	b := make([]any, 0, len(a))
	for _, v := range a {
		b = append(b, v)
	}
	_, err := db.ExecContext(ctx, fmt.Sprintf("UPDATE rides SET chair_id = ELT(FIELD(id%s)%s) WHERE id IN (%s)", strings.Repeat(",?", len(rideIDs)), strings.Repeat(",?", len(chairIDs)), "?"+strings.Repeat(",?", len(rideIDs)-1)), b...)
	if err != nil {
		slog.Error("failed to update ride", "error", err)
		return fmt.Errorf("failed to update ride: %w", err)
	}

	for _, ride := range rides {
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
