-- Phase 1: fare engine inputs.
-- Recalibrate per-km base fares so quotes land near real Indian Railways fares
-- (e.g. Chennai → Ernakulam, Sleeper ≈ ₹400, 3A ≈ ₹1,050), and add the minimum
-- distance a ticket in each class is charged for.

ALTER TABLE coach_classes ADD COLUMN min_distance_km int NOT NULL DEFAULT 0;

UPDATE coach_classes SET fare_per_km = v.rate, min_distance_km = v.min_km
  FROM (VALUES
    ('1A', 3.500, 300), ('EC', 2.600, 150), ('2A', 2.100, 300), ('3A', 1.450, 300),
    ('3E', 1.350, 300), ('CC', 1.300, 150), ('SL', 0.550, 200), ('2S', 0.300, 50)
  ) AS v(code, rate, min_km)
 WHERE coach_classes.code = v.code;
