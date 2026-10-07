-- A fingerprint of a KamaraPMS database: for every table of the public schema, the row count and a hash of the rows.
-- Run it on the source and on the restored copy; the two outputs must be identical:
--
--   docker exec -i <container> psql -U pms -d pms -qAt < scripts/db-verify.sql > source.txt
--
-- Output: one line per table "table|rows|hash", then "migration|<version>", "tables|<n>" and "total_rows|<n>". Read only.
select format('%s|%s|%s', t.table_name, x.rows, x.hash)
from information_schema.tables t
cross join lateral (
  select (xpath('/row/rows/text()', query_to_xml(format('select count(*) as rows from public.%I', t.table_name), false, true, '')))[1]::text as rows,
         (xpath('/row/hash/text()', query_to_xml(format('select coalesce(md5(string_agg(r::text, '''' order by r::text)), ''-'') as hash from public.%I r', t.table_name), false, true, '')))[1]::text as hash
) x
where t.table_schema = 'public' and t.table_type = 'BASE TABLE' and t.table_name <> 'goose_db_version'
order by t.table_name;
select 'migration|' || max(version_id) from goose_db_version;
select 'tables|' || count(*) from pg_tables where schemaname = 'public';
select 'total_rows|' || sum(c) from (
  select (xpath('/row/c/text()', query_to_xml(format('select count(*) as c from public.%I', table_name), false, true, '')))[1]::text::bigint as c
  from information_schema.tables where table_schema = 'public' and table_type = 'BASE TABLE') s;
