INSERT INTO chats(jid,name,type,parent_jid) VALUES('10000000100@g.us','','','');
INSERT INTO participants(jid,push_name,contact_name) VALUES('20000000001@lid','Alice','');
INSERT INTO participants(jid,push_name,contact_name) VALUES('20000000002@lid','Bob','');
INSERT INTO id_mappings(lid,pn,source) VALUES('20000000001@lid','10000000001@s.whatsapp.net','message_alt');
INSERT INTO id_mappings(lid,pn,source) VALUES('20000000002@lid','10000000002@s.whatsapp.net','message_alt');
INSERT INTO messages(chat_jid,id,sender_jid,from_me,push_name,kind,text,caption,quoted_id,quoted_sender,mentions,forwarded,forward_score,view_once,ephemeral_secs,details,source,ts,raw_proto) VALUES('10000000100@g.us','K-UNKNOWN-1','20000000001@lid',0,'Alice','unknown','','','','','[]',0,0,0,0,'{"fields":["groupInviteMessage"]}','live',1760000060,X'E2010F220D416E6F746865722067726F7570');
INSERT INTO messages(chat_jid,id,sender_jid,from_me,push_name,kind,text,caption,quoted_id,quoted_sender,mentions,forwarded,forward_score,view_once,ephemeral_secs,details,source,ts,raw_proto) VALUES('10000000100@g.us','K-UNKNOWN-2','20000000002@lid',0,'Bob','unknown','','','','','[]',0,0,0,0,'{"fields":["groupInviteMessage","orderMessage"]}','live',1760000120,X'E20100B20200');
INSERT INTO messages(chat_jid,id,sender_jid,from_me,push_name,kind,text,caption,quoted_id,quoted_sender,mentions,forwarded,forward_score,view_once,ephemeral_secs,details,source,ts,raw_proto) VALUES('10000000100@g.us','K-UNKNOWN-3','20000000001@lid',0,'Alice','unknown','','','','','[]',0,0,0,0,'{}','live',1760000180,X'');
