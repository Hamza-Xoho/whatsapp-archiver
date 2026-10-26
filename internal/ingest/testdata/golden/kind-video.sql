INSERT INTO chats(jid,name,type,parent_jid) VALUES('10000000100@g.us','','','');
INSERT INTO participants(jid,push_name,contact_name) VALUES('20000000001@lid','Alice','');
INSERT INTO participants(jid,push_name,contact_name) VALUES('20000000002@lid','Bob','');
INSERT INTO id_mappings(lid,pn,source) VALUES('20000000001@lid','10000000001@s.whatsapp.net','message_alt');
INSERT INTO id_mappings(lid,pn,source) VALUES('20000000002@lid','10000000002@s.whatsapp.net','message_alt');
INSERT INTO messages(chat_jid,id,sender_jid,from_me,push_name,kind,text,caption,quoted_id,quoted_sender,mentions,forwarded,forward_score,view_once,ephemeral_secs,details,source,ts,raw_proto) VALUES('10000000100@g.us','K-VIDEO-1','20000000001@lid',0,'Alice','video','','A clip','','','[]',0,0,0,0,'{}','live',1760000060,X'4A2F1209766964656F2F6D703420A0F736281F3A064120636C697048800A50D00582010DFFD8FFE000104A46494600FFD9');
INSERT INTO messages(chat_jid,id,sender_jid,from_me,push_name,kind,text,caption,quoted_id,quoted_sender,mentions,forwarded,forward_score,view_once,ephemeral_secs,details,source,ts,raw_proto) VALUES('10000000100@g.us','K-VIDEO-2','20000000002@lid',0,'Bob','video','','','','','[]',0,0,0,0,'{"ptv":true}','live',1760000120,X'9204131209766964656F2F6D7034280948E00350E003');
