ALTER TABLE ride_statuses ADD INDEX IX_ride_statuses_ride_id_created_at (ride_id, created_at);
ALTER TABLE ride_statuses ADD INDEX IX_ride_statuses_ride_id_chair_sent_at_created_at (ride_id, chair_sent_at, created_at);
ALTER TABLE chair_locations ADD INDEX IX_chair_locations_chair_id_created_at (chair_id, created_at);
ALTER TABLE chair_locations ADD INDEX IX_chair_locations_chair_id_id (chair_id, id);
ALTER TABLE rides ADD INDEX IX_rides_chair_id_updated_at (chair_id, updated_at);
ALTER TABLE rides ADD INDEX IX_rides_evaluation (evaluation);
ALTER TABLE chairs ADD INDEX IX_chairs_access_token (access_token);

DROP TABLE IF EXISTS chair_locations_minus_distance;
CREATE TABLE chair_locations_minus_distance
(
  id         VARCHAR(26) NOT NULL,
  chair_id   VARCHAR(26) NOT NULL COMMENT '椅子ID',
  distance   LONG        NOT NULL COMMENT 'マイナスされた距離',
  PRIMARY KEY (id)
)
  COMMENT = '椅子のマイナスされた距離テーブル';

ALTER TABLE chair_locations_minus_distance ADD INDEX IX_chair_locations_minus_distance_chair_id (chair_id);
