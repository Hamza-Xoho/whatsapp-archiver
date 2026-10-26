INSERT INTO chats(jid,name,type,parent_jid) VALUES('10000000100@g.us','','','');
INSERT INTO participants(jid,push_name,contact_name) VALUES('20000000001@lid','Alice','');
INSERT INTO id_mappings(lid,pn,source) VALUES('20000000001@lid','10000000001@s.whatsapp.net','message_alt');
INSERT INTO messages(chat_jid,id,sender_jid,from_me,push_name,kind,text,caption,quoted_id,quoted_sender,mentions,forwarded,forward_score,view_once,ephemeral_secs,details,source,ts,raw_proto) VALUES('10000000100@g.us','K-AUDIO-1','20000000001@lid',0,'Alice','audio','','','','','[]',0,0,0,0,'{}','live',1760000060,X'4214120A617564696F2F6D70656720A080B10128B801');
