INSERT INTO chats(jid,name,type,parent_jid) VALUES('10000000100@g.us','','','');
INSERT INTO participants(jid,push_name,contact_name) VALUES('20000000001@lid','','');
INSERT INTO participants(jid,push_name,contact_name) VALUES('20000000002@lid','','');
INSERT INTO message_events(chat_jid,target_id,kind,event_id,source_id,cause,sender_jid,source,ts,new_text,value,raw_proto) VALUES('10000000100@g.us','U-HIDDEN-1','undecryptable','7252e876691d29b9196d9aad','','other','20000000001@lid','live',1760000240,'','hide',X'');
INSERT INTO message_events(chat_jid,target_id,kind,event_id,source_id,cause,sender_jid,source,ts,new_text,value,raw_proto) VALUES('10000000100@g.us','U-NOKEY-1','undecryptable','cd458da0def0ebecd92d82df','','no_sender_key','20000000002@lid','live',1760000120,'','',X'');
INSERT INTO message_events(chat_jid,target_id,kind,event_id,source_id,cause,sender_jid,source,ts,new_text,value,raw_proto) VALUES('10000000100@g.us','U-OTHER-1','undecryptable','aded2b5bc87e3cc50eff948a','','other','20000000002@lid','live',1760000060,'','',X'');
INSERT INTO message_events(chat_jid,target_id,kind,event_id,source_id,cause,sender_jid,source,ts,new_text,value,raw_proto) VALUES('10000000100@g.us','U-VIEWONCE-1','undecryptable','5613ba73cfd05f48b306b5bc','','view_once_unavailable','20000000001@lid','live',1760000180,'','',X'');
