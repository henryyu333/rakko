CREATE TABLE message (
  id TEXT PRIMARY KEY,
  session_id TEXT NOT NULL,
  time_created INTEGER NOT NULL,
  time_updated INTEGER NOT NULL,
  data TEXT NOT NULL
);

INSERT INTO message VALUES
  ('msg_1', 'ses_1', 1792404000000, 1792404001000,
   '{"role":"assistant","modelID":"deepseek-v4-flash","providerID":"deepseek","tokens":{"total":135,"input":100,"output":10,"reasoning":5,"cache":{"write":2,"read":20}},"cost":0.01}'),
  ('msg_2', 'ses_1', 1792407600000, 1792407601000,
   '{"role":"assistant","modelID":"deepseek-v4-flash","providerID":"deepseek","tokens":{"total":264,"input":200,"output":20,"reasoning":0,"cache":{"write":4,"read":40}},"cost":0.02}'),
  ('msg_3', 'ses_1', 1792411200000, 1792411201000,
   '{"role":"user","modelID":"deepseek-v4-flash","providerID":"deepseek"}'),
  ('msg_4', 'ses_1', 1792411800000, 1792411801000,
   '{"role":"assistant","modelID":"deepseek-v4-flash","providerID":"deepseek"}');
