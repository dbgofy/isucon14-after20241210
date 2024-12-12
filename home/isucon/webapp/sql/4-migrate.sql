ALTER TABLE ride_statuses ADD INDEX IX_ride_statuses_ride_id_created_at (ride_id, created_at);
ALTER TABLE ride_statuses ADD UNIQUE INDEX UQ_ride_statuses_ride_id_status (ride_id, status);
ALTER TABLE ride_statuses ADD INDEX IX_ride_statuses_ride_id_chair_sent_at_created_at (ride_id, chair_sent_at, created_at);
ALTER TABLE ride_statuses ADD INDEX IX_ride_statuses_ride_id_app_sent_at_created_at (ride_id, app_sent_at, created_at);
ALTER TABLE chair_locations ADD INDEX IX_chair_locations_chair_id_created_at (chair_id, created_at);
ALTER TABLE chair_locations ADD INDEX IX_chair_locations_chair_id_id (chair_id, id);
ALTER TABLE rides ADD INDEX IX_rides_chair_id_updated_at (chair_id, updated_at);
ALTER TABLE rides ADD INDEX IX_rides_chair_id_created_at (chair_id, created_at);
ALTER TABLE rides ADD INDEX IX_rides_user_id_created_at (user_id, created_at);
ALTER TABLE rides ADD INDEX IX_rides_evaluation_chair_id_updated_at (evaluation, chair_id, updated_at);
ALTER TABLE chairs ADD INDEX IX_chairs_access_token (access_token);
ALTER TABLE chairs ADD INDEX IX_chairs_owner_id (owner_id);
ALTER TABLE coupons ADD INDEX IX_coupons_code (code);

DROP TABLE IF EXISTS chair_locations_total_distance;
CREATE TABLE chair_locations_total_distance
(
  chair_id         VARCHAR(26) NOT NULL COMMENT '椅子ID',
  total_distance   LONG        NOT NULL COMMENT '合計移動距離距離',
  PRIMARY KEY (chair_id)
)
  COMMENT = '合計移動距離距離テーブル';

ALTER TABLE chair_locations_total_distance ADD INDEX IX_chair_locations_total_distance_chair_id (chair_id);
