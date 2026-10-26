INSERT INTO chats(jid,name,type,parent_jid) VALUES('10000000100@g.us','','','');
INSERT INTO participants(jid,push_name,contact_name) VALUES('20000000001@lid','Alice','');
INSERT INTO id_mappings(lid,pn,source) VALUES('20000000001@lid','10000000001@s.whatsapp.net','message_alt');
INSERT INTO messages(chat_jid,id,sender_jid,from_me,push_name,kind,text,caption,quoted_id,quoted_sender,mentions,forwarded,forward_score,view_once,ephemeral_secs,details,source,ts,raw_proto) VALUES('10000000100@g.us','K-DOC-1','20000000001@lid',0,'Alice','document','','Syllabus for term 2','','','[]',0,0,0,0,'{}','live',1760000060,X'AA034F0A4D3A4B120F6170706C69636174696F6E2F70646628C8D904300E420C73796C6C616275732E70646682010DFFD8FFE000104A46494600FFD9A2011353796C6C6162757320666F72207465726D2032');
