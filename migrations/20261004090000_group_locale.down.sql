ALTER TABLE groups
  DROP COLUMN locale_date_format,
  DROP COLUMN locale_time_format,
  DROP COLUMN locale_timezone,
  DROP COLUMN locale_number_format;

DROP TYPE group_date_format;
DROP TYPE group_time_format;
DROP TYPE group_timezone;
DROP TYPE group_number_format;
