INSERT INTO chats(jid,name,type,parent_jid) VALUES('10000000100@g.us','','','');
INSERT INTO participants(jid,push_name,contact_name) VALUES('20000000001@lid','Alice','');
INSERT INTO participants(jid,push_name,contact_name) VALUES('20000000002@lid','Bob','');
INSERT INTO id_mappings(lid,pn,source) VALUES('20000000001@lid','10000000001@s.whatsapp.net','message_alt');
INSERT INTO id_mappings(lid,pn,source) VALUES('20000000002@lid','10000000002@s.whatsapp.net','message_alt');
INSERT INTO messages(chat_jid,id,sender_jid,from_me,push_name,kind,text,caption,quoted_id,quoted_sender,mentions,forwarded,forward_score,view_once,ephemeral_secs,details,source,ts,raw_proto) VALUES('10000000100@g.us','K-IMAGE-1','20000000001@lid',0,'Alice','image','','A photo','','','[]',0,0,0,0,'{}','live',1760000060,X'1A4E120A696D6167652F6A7065671A07412070686F746F28801030E0033880055A1E2F762F7436322E373131382D32342F73796E7468657469632D696D61676582010DFFD8FFE000104A46494600FFD9');
INSERT INTO messages(chat_jid,id,sender_jid,from_me,push_name,kind,text,caption,quoted_id,quoted_sender,mentions,forwarded,forward_score,view_once,ephemeral_secs,details,source,ts,raw_proto) VALUES('10000000100@g.us','K-IMAGE-2','20000000002@lid',0,'Bob','image','','','','','[]',0,0,1,0,'{}','live',1760000120,X'BA03160A141A12120A696D6167652F6A706567288008C80101');
