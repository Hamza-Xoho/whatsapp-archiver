INSERT INTO chats(jid,name,type,parent_jid) VALUES('10000000100@g.us','','','');
INSERT INTO participants(jid,push_name,contact_name) VALUES('20000000001@lid','Alice','');
INSERT INTO participants(jid,push_name,contact_name) VALUES('20000000002@lid','Bob','');
INSERT INTO id_mappings(lid,pn,source) VALUES('20000000001@lid','10000000001@s.whatsapp.net','message_alt');
INSERT INTO id_mappings(lid,pn,source) VALUES('20000000002@lid','10000000002@s.whatsapp.net','message_alt');
INSERT INTO messages(chat_jid,id,sender_jid,from_me,push_name,kind,text,caption,quoted_id,quoted_sender,mentions,forwarded,forward_score,view_once,ephemeral_secs,details,source,ts,raw_proto) VALUES('10000000100@g.us','K-SYSTEM-1','20000000001@lid',0,'Alice','system','','','','','[]',0,0,0,0,'{"type":"callLogMesssage"}','live',1760000060,X'AA0400');
INSERT INTO messages(chat_jid,id,sender_jid,from_me,push_name,kind,text,caption,quoted_id,quoted_sender,mentions,forwarded,forward_score,view_once,ephemeral_secs,details,source,ts,raw_proto) VALUES('10000000100@g.us','K-SYSTEM-2','20000000002@lid',0,'Bob','system','','','','','[]',0,0,0,0,'{"type":"scheduledCallCreationMessage"}','live',1760000120,X'EA03091A075374616E647570');
