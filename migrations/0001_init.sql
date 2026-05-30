CREATE EXTENSION IF NOT EXISTS btree_gist;

CREATE TABLE customer (
  id   uuid PRIMARY KEY,
  name text NOT NULL
);

CREATE TABLE dealership (
  id   uuid PRIMARY KEY,
  name text NOT NULL
);

CREATE TABLE vehicle (
  id          uuid PRIMARY KEY,
  customer_id uuid NOT NULL REFERENCES customer(id),
  vin         text NOT NULL,
  make        text NOT NULL,
  model       text NOT NULL
);

CREATE TABLE skill (
  id   uuid PRIMARY KEY,
  name text NOT NULL UNIQUE
);

CREATE TABLE service_bay (
  id            uuid PRIMARY KEY,
  dealership_id uuid NOT NULL REFERENCES dealership(id),
  name          text NOT NULL
);

CREATE TABLE technician (
  id            uuid PRIMARY KEY,
  dealership_id uuid NOT NULL REFERENCES dealership(id),
  name          text NOT NULL
);

CREATE TABLE technician_skill (
  technician_id uuid NOT NULL REFERENCES technician(id),
  skill_id      uuid NOT NULL REFERENCES skill(id),
  PRIMARY KEY (technician_id, skill_id)
);

CREATE TABLE service_type (
  id                uuid PRIMARY KEY,
  name              text NOT NULL,
  duration_minutes  int  NOT NULL CHECK (duration_minutes > 0),
  required_skill_id uuid NOT NULL REFERENCES skill(id)
);

CREATE TABLE appointment (
  id              uuid PRIMARY KEY,
  dealership_id   uuid NOT NULL REFERENCES dealership(id),
  customer_id     uuid NOT NULL REFERENCES customer(id),
  vehicle_id      uuid NOT NULL REFERENCES vehicle(id),
  service_type_id uuid NOT NULL REFERENCES service_type(id),
  technician_id   uuid NOT NULL REFERENCES technician(id),
  service_bay_id  uuid NOT NULL REFERENCES service_bay(id),
  during          tstzrange NOT NULL,
  status          text NOT NULL DEFAULT 'confirmed',

  -- Source of truth: Two confirmed appointments cannot occupy the same service bay at the same time.
  CONSTRAINT no_bay_overlap
    EXCLUDE USING gist (service_bay_id WITH =, during WITH &&) WHERE (status = 'confirmed'),
  -- ...nor can they be assigned to the same technician simultaneously.
  CONSTRAINT no_technician_overlap
    EXCLUDE USING gist (technician_id WITH =, during WITH &&) WHERE (status = 'confirmed')
);