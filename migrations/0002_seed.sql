INSERT INTO dealership (id, name) VALUES
  ('11111111-1111-1111-1111-111111111111', 'Downtown Auto');

INSERT INTO customer (id, name) VALUES
  ('22222222-2222-2222-2222-222222222222', 'Tran Gia Thuan');

INSERT INTO vehicle (id, customer_id, vin, make, model) VALUES
  ('33333333-3333-3333-3333-333333333333',
   '22222222-2222-2222-2222-222222222222',
   '1HGCM82633A004352', 'Honda', 'Accord');

INSERT INTO skill (id, name) VALUES
  ('aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa', 'brakes'),
  ('bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb', 'engine');

INSERT INTO service_bay (id, dealership_id, name) VALUES
  ('44444444-4444-4444-4444-444444444444', '11111111-1111-1111-1111-111111111111', 'Bay 1'),
  ('55555555-5555-5555-5555-555555555555', '11111111-1111-1111-1111-111111111111', 'Bay 2');

INSERT INTO technician (id, dealership_id, name) VALUES
  ('66666666-6666-6666-6666-666666666666', '11111111-1111-1111-1111-111111111111', 'Alice'),
  ('77777777-7777-7777-7777-777777777777', '11111111-1111-1111-1111-111111111111', 'Bob');

INSERT INTO technician_skill (technician_id, skill_id) VALUES
  ('66666666-6666-6666-6666-666666666666', 'aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa'), -- Alice: brakes
  ('77777777-7777-7777-7777-777777777777', 'bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb'); -- Bob: engine

INSERT INTO service_type (id, name, duration_minutes, required_skill_id) VALUES
  ('88888888-8888-8888-8888-888888888888', 'Brake Service',     60, 'aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa'),
  ('99999999-9999-9999-9999-999999999999', 'Engine Diagnostic', 90, 'bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb');