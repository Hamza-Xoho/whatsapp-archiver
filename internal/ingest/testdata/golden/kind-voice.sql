INSERT INTO chats(jid,name,type,parent_jid) VALUES('10000000100@g.us','','','');
INSERT INTO participants(jid,push_name,contact_name) VALUES('20000000002@lid','Bob','');
INSERT INTO id_mappings(lid,pn,source) VALUES('20000000002@lid','10000000002@s.whatsapp.net','message_alt');
INSERT INTO messages(chat_jid,id,sender_jid,from_me,push_name,kind,text,caption,quoted_id,quoted_sender,mentions,forwarded,forward_score,view_once,ephemeral_secs,details,source,ts,raw_proto) VALUES('10000000100@g.us','K-VOICE-1','20000000002@lid',0,'Bob','voice','','','','','[]',0,0,0,0,'{}','live',1760000060,X'42201216617564696F2F6F67673B20636F646563733D6F70757320D08C01280C3001');
