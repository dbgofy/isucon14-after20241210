ALTER TABLE ride_statuses ADD INDEX IX_ride_statuses_ride_id_created_at (ride_id, created_at);
ALTER TABLE ride_statuses ADD INDEX IX_ride_statuses_ride_id_chair_sent_at_created_at (ride_id, chair_sent_at, created_at);
ALTER TABLE chair_locations ADD INDEX IX_chair_locations_chair_id_created_at (chair_id, created_at);
ALTER TABLE rides ADD INDEX IX_rides_chair_id_updated_at (chair_id, updated_at);
ALTER TABLE chairs ADD INDEX IX_chairs_access_token (access_token);
