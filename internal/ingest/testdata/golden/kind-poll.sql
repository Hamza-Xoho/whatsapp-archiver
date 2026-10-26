INSERT INTO chats(jid,name,type,parent_jid) VALUES('10000000100@g.us','','','');
INSERT INTO participants(jid,push_name,contact_name) VALUES('20000000001@lid','Alice','');
INSERT INTO participants(jid,push_name,contact_name) VALUES('20000000002@lid','Bob','');
INSERT INTO id_mappings(lid,pn,source) VALUES('20000000001@lid','10000000001@s.whatsapp.net','message_alt');
INSERT INTO id_mappings(lid,pn,source) VALUES('20000000002@lid','10000000002@s.whatsapp.net','message_alt');
INSERT INTO messages(chat_jid,id,sender_jid,from_me,push_name,kind,text,caption,quoted_id,quoted_sender,mentions,forwarded,forward_score,view_once,ephemeral_secs,details,source,ts,raw_proto) VALUES('10000000100@g.us','K-POLL-1','20000000001@lid',0,'Alice','poll','Lunch?','','','','[]',0,0,0,0,'{"question":"Lunch?","options":["Yes","No","غداً"],"max":1}','live',1760000060,X'8A032312064C756E63683F1A050A035965731A040A024E6F1A0A0A08D8BAD8AFD8A7D98B2001');
INSERT INTO messages(chat_jid,id,sender_jid,from_me,push_name,kind,text,caption,quoted_id,quoted_sender,mentions,forwarded,forward_score,view_once,ephemeral_secs,details,source,ts,raw_proto) VALUES('10000000100@g.us','K-POLL-2','20000000002@lid',0,'Bob','poll','Where?','','','','[]',0,0,0,0,'{"question":"Where?","options":["Here","There"]}','live',1760000120,X'820419120657686572653F1A060A04486572651A070A055468657265');
