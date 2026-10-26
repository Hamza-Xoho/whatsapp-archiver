INSERT INTO chats(jid,name,type,parent_jid) VALUES('10000000100@g.us','','','');
INSERT INTO participants(jid,push_name,contact_name) VALUES('20000000001@lid','Alice','');
INSERT INTO participants(jid,push_name,contact_name) VALUES('20000000002@lid','Bob','');
INSERT INTO id_mappings(lid,pn,source) VALUES('20000000001@lid','10000000001@s.whatsapp.net','message_alt');
INSERT INTO id_mappings(lid,pn,source) VALUES('20000000002@lid','10000000002@s.whatsapp.net','message_alt');
INSERT INTO messages(chat_jid,id,sender_jid,from_me,push_name,kind,text,caption,quoted_id,quoted_sender,mentions,forwarded,forward_score,view_once,ephemeral_secs,details,source,ts,raw_proto) VALUES('10000000100@g.us','K-LIVE-1','20000000001@lid',0,'Alice','live_location','','On my way','','','[]',0,0,0,0,'{"lat":24.5,"lon":46.5,"accuracy_m":12,"speed":3.5,"sequence":7}','live',1760000060,X'920126090000000000803840110000000000404740180C250000604032094F6E206D79207761793807');
INSERT INTO messages(chat_jid,id,sender_jid,from_me,push_name,kind,text,caption,quoted_id,quoted_sender,mentions,forwarded,forward_score,view_once,ephemeral_secs,details,source,ts,raw_proto) VALUES('10000000100@g.us','K-LIVE-2','20000000002@lid',0,'Bob','live_location','','','','','[]',0,0,0,0,'{"lat":24.6,"lon":46.6}','live',1760000120,X'920112099A9999999999384011CDCCCCCCCC4C4740');
