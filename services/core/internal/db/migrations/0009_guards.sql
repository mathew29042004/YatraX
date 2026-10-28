-- A berth allocation always covers at least one segment. An empty range has no lower
-- bound, which the occupancy loader can't read; refuse it at the source.
ALTER TABLE seat_allocations ADD CONSTRAINT seat_allocations_not_empty CHECK (NOT isempty(seg));
