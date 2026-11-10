-- Design Cloud Engine — PostgreSQL: 0.1.x -> 0.2.x（现行 model，唯一迁移步骤）
-- 说明：覆盖 0.1.x 到当前 internal/model 的全量 PostgreSQL 存量库升级：
--       表改名、node_liveness 拆表、字段重命名/类型收敛、布尔极性反转、废弃列/索引/设置域清理、
--       users 删除登录 IP、connections 合并为 last_active_at、
--       user_connection_resources / user_apps 授权键改为 username、
--       以及 §13 新增表/列/索引（绿库由引擎 AutoMigrate 建表；存量须本脚本对齐后再启动）。
-- 幂等：使用 to_regclass / information_schema 判断，可安全重跑。
-- 注意：0.1.x 存量库必须先完成本脚本再启动 dc-engine；全新空库可跳过本脚本。

BEGIN;

-- 1. ban_records -> bans
DO $$
BEGIN
    IF to_regclass('public.ban_records') IS NOT NULL AND to_regclass('public.bans') IS NULL THEN
        ALTER TABLE ban_records RENAME TO bans;
    END IF;

    IF to_regclass('public.bans') IS NOT NULL THEN
        DROP INDEX IF EXISTS idx_ban_username;

        IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'bans' AND column_name = 'type')
           AND NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'bans' AND column_name = 'subject') THEN
            ALTER TABLE bans RENAME COLUMN type TO subject;
        END IF;

        IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'bans' AND column_name = 'created_at')
           AND NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'bans' AND column_name = 'first_failed_at') THEN
            ALTER TABLE bans RENAME COLUMN created_at TO first_failed_at;
        END IF;

        IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'bans' AND column_name = 'updated_at')
           AND NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'bans' AND column_name = 'last_failed_at') THEN
            ALTER TABLE bans RENAME COLUMN updated_at TO last_failed_at;
        END IF;

        ALTER TABLE bans ADD COLUMN IF NOT EXISTS subject varchar(16);
        ALTER TABLE bans ALTER COLUMN subject TYPE varchar(16);
        ALTER TABLE bans ADD COLUMN IF NOT EXISTS source varchar(16) DEFAULT 'Login';
        ALTER TABLE bans ADD COLUMN IF NOT EXISTS reason varchar(255) DEFAULT '';

        UPDATE bans SET subject = 'UserIP' WHERE subject = 'User';
        UPDATE bans SET source = 'Login' WHERE source IS NULL OR source = '';
    END IF;
END $$;

-- 2. downloads -> download_requests
DO $$
BEGIN
    IF to_regclass('public.downloads') IS NOT NULL AND to_regclass('public.download_requests') IS NULL THEN
        ALTER TABLE downloads RENAME TO download_requests;
    END IF;

    IF to_regclass('public.download_requests') IS NOT NULL THEN
        DROP INDEX IF EXISTS idx_downloads_requester;
        DROP INDEX IF EXISTS idx_downloads_status;
        DROP INDEX IF EXISTS idx_downloads_deleted_at;
    END IF;
END $$;

-- 3. node_resource_reports -> node_metrics（移除 id，主键改为 node_id）
DO $$
DECLARE
    pk_name text;
BEGIN
    IF to_regclass('public.node_resource_reports') IS NOT NULL AND to_regclass('public.node_metrics') IS NULL THEN
        ALTER TABLE node_resource_reports RENAME TO node_metrics;
    END IF;

    IF to_regclass('public.node_metrics') IS NOT NULL THEN
        IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'node_metrics' AND column_name = 'id') THEN
            DELETE FROM node_metrics a
            USING node_metrics b
            WHERE a.node_id = b.node_id
              AND (a.reported_at < b.reported_at OR (a.reported_at = b.reported_at AND a.id < b.id));

            SELECT c.conname INTO pk_name
            FROM pg_constraint c
            WHERE c.conrelid = 'public.node_metrics'::regclass AND c.contype = 'p';
            IF pk_name IS NOT NULL THEN
                EXECUTE format('ALTER TABLE public.node_metrics DROP CONSTRAINT %I', pk_name);
            END IF;

            DROP INDEX IF EXISTS idx_node_resource_reports_node_id;
            ALTER TABLE node_metrics DROP COLUMN id;
            ALTER TABLE node_metrics ADD PRIMARY KEY (node_id);
        END IF;
    END IF;
END $$;

-- 4. apps（清理软删记录及其 user_apps 关联，删除 deleted_at）
DO $$
BEGIN
    IF to_regclass('public.apps') IS NOT NULL
       AND EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'apps' AND column_name = 'deleted_at') THEN
        IF to_regclass('public.user_apps') IS NOT NULL THEN
            DELETE FROM user_apps
            WHERE app_id IN (SELECT id FROM apps WHERE deleted_at IS NOT NULL);
        END IF;
        DELETE FROM apps WHERE deleted_at IS NOT NULL;
        DROP INDEX IF EXISTS idx_apps_deleted_at;
        ALTER TABLE apps DROP COLUMN deleted_at;
    END IF;
END $$;

-- 5. node_liveness 拆表与 nodes 表收敛
CREATE TABLE IF NOT EXISTS node_liveness (
    node_id              varchar(64) PRIMARY KEY,
    online               boolean     NOT NULL DEFAULT FALSE,
    last_heartbeat_at    timestamptz,
    last_connected_at    timestamptz,
    last_disconnected_at timestamptz
);
CREATE INDEX IF NOT EXISTS idx_node_liveness_online_hb ON node_liveness (online, last_heartbeat_at);
CREATE INDEX IF NOT EXISTS idx_node_liveness_last_connected_at ON node_liveness (last_connected_at);

DO $$
BEGIN
    IF to_regclass('public.nodes') IS NOT NULL THEN
        -- 5.1 搬迁运行态至 node_liveness（0.1.21 中 status: 1=online, 2=offline, 3=pending, 4=provisioned, 5=maintenance）
        IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'nodes' AND column_name = 'status')
           AND EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'nodes' AND column_name = 'connected_at') THEN
            INSERT INTO node_liveness (node_id, online, last_heartbeat_at, last_connected_at, last_disconnected_at)
            SELECT
                node_id,
                (status = 1),
                last_heartbeat_at,
                connected_at,
                disconnected_at
            FROM nodes
            WHERE status IN (1, 2, 5)
               OR (connected_at IS NOT NULL AND connected_at > '0001-01-02'::timestamptz)
               OR (last_heartbeat_at IS NOT NULL AND last_heartbeat_at > '0001-01-02'::timestamptz)
               OR (disconnected_at IS NOT NULL AND disconnected_at > '0001-01-02'::timestamptz)
            ON CONFLICT (node_id) DO NOTHING;
        END IF;

        -- 5.2 0.1.21 中 status=5 (maintenance) -> in_maintenance=true
        ALTER TABLE nodes ADD COLUMN IF NOT EXISTS in_maintenance boolean NOT NULL DEFAULT FALSE;
        IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'nodes' AND column_name = 'status') THEN
            UPDATE nodes SET in_maintenance = TRUE WHERE status = 5;
        END IF;

        -- 5.3 字段重命名：client_host -> dial_host, node_settings -> settings
        IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'nodes' AND column_name = 'client_host')
           AND NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'nodes' AND column_name = 'dial_host') THEN
            ALTER TABLE nodes RENAME COLUMN client_host TO dial_host;
        END IF;

        IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'nodes' AND column_name = 'node_settings')
           AND NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'nodes' AND column_name = 'settings') THEN
            ALTER TABLE nodes RENAME COLUMN node_settings TO settings;
        END IF;

        -- 5.4 labels (JSON map) -> groups (JSON array)，并删除 labels
        ALTER TABLE nodes ADD COLUMN IF NOT EXISTS groups text;
        IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'nodes' AND column_name = 'labels') THEN
            UPDATE nodes
            SET groups = COALESCE(
                (
                    SELECT jsonb_agg(lower(trim(k)) ORDER BY lower(trim(k)))::text
                    FROM jsonb_object_keys(labels::jsonb) AS k
                    WHERE trim(k) <> ''
                ),
                '[]'
            )
            WHERE labels IS NOT NULL
              AND trim(labels) ~ '^\{.*\}$';
            ALTER TABLE nodes DROP COLUMN labels;
        END IF;

        -- 5.5 布尔极性反转：cert_auto_renew -> cert_auto_renew_disabled, allow_connections -> connections_disabled
        ALTER TABLE nodes ADD COLUMN IF NOT EXISTS cert_auto_renew_disabled boolean NOT NULL DEFAULT FALSE;
        IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'nodes' AND column_name = 'cert_auto_renew') THEN
            UPDATE nodes SET cert_auto_renew_disabled = NOT COALESCE(cert_auto_renew, TRUE);
            ALTER TABLE nodes DROP COLUMN cert_auto_renew;
        END IF;

        ALTER TABLE nodes ADD COLUMN IF NOT EXISTS connections_disabled boolean NOT NULL DEFAULT FALSE;
        IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'nodes' AND column_name = 'allow_connections') THEN
            UPDATE nodes SET connections_disabled = NOT COALESCE(allow_connections, TRUE);
            ALTER TABLE nodes DROP COLUMN allow_connections;
        END IF;

        -- 5.6 新增字段
        ALTER TABLE nodes ADD COLUMN IF NOT EXISTS dial_host varchar(255);
        ALTER TABLE nodes ADD COLUMN IF NOT EXISTS arch varchar(32);
        ALTER TABLE nodes ADD COLUMN IF NOT EXISTS os_name varchar(128);
        ALTER TABLE nodes ADD COLUMN IF NOT EXISTS os_version varchar(64);
        ALTER TABLE nodes ADD COLUMN IF NOT EXISTS kernel_version varchar(128);
        ALTER TABLE nodes ADD COLUMN IF NOT EXISTS description varchar(512) NOT NULL DEFAULT '';
        ALTER TABLE nodes ADD COLUMN IF NOT EXISTS settings text;
        ALTER TABLE nodes ADD COLUMN IF NOT EXISTS enrollment_token_hash varchar(64);
        ALTER TABLE nodes ADD COLUMN IF NOT EXISTS enrollment_token_expires_at timestamptz;

        -- 5.7 清理 nodes.settings 节点覆盖 JSON 中的旧键名/废弃键（NodeSettings 严格禁止未知字段）
        UPDATE nodes
        SET settings = jsonb_set(
            settings::jsonb #- '{agent,launcherPath}',
            '{agent,execBinPath}',
            settings::jsonb #> '{agent,launcherPath}'
        )::text
        WHERE settings IS NOT NULL
          AND trim(settings) ~ '^\{.*\}$'
          AND (settings::jsonb #> '{agent}') ? 'launcherPath';

        UPDATE nodes
        SET settings = jsonb_set(
            settings::jsonb #- '{agent,vnc,useCgroup}',
            '{agent,vnc,cgroupEnabled}',
            settings::jsonb #> '{agent,vnc,useCgroup}'
        )::text
        WHERE settings IS NOT NULL
          AND trim(settings) ~ '^\{.*\}$'
          AND (settings::jsonb #> '{agent,vnc}') ? 'useCgroup';

        UPDATE nodes
        SET settings = (
            settings::jsonb
            #- '{agent,monitor,resourceReportEnabled}'
            #- '{agent,monitor,resourceReportInterval}'
            #- '{alert,cpu,cooldownInterval}'
            #- '{alert,load,cooldownInterval}'
            #- '{alert,memory,cooldownInterval}'
            #- '{alert,disk,cooldownInterval}'
        )::text
        WHERE settings IS NOT NULL
          AND trim(settings) ~ '^\{.*\}$'
          AND (
              (settings::jsonb #> '{agent,monitor}') ?| ARRAY['resourceReportEnabled', 'resourceReportInterval']
              OR (settings::jsonb #> '{alert,cpu}') ? 'cooldownInterval'
              OR (settings::jsonb #> '{alert,load}') ? 'cooldownInterval'
              OR (settings::jsonb #> '{alert,memory}') ? 'cooldownInterval'
              OR (settings::jsonb #> '{alert,disk}') ? 'cooldownInterval'
          );

        -- 5.8 删除已废弃与已拆表列
        DROP INDEX IF EXISTS idx_nodes_status;
        ALTER TABLE nodes DROP COLUMN IF EXISTS status;
        ALTER TABLE nodes DROP COLUMN IF EXISTS last_heartbeat_at;
        ALTER TABLE nodes DROP COLUMN IF EXISTS connected_at;
        ALTER TABLE nodes DROP COLUMN IF EXISTS disconnected_at;
        ALTER TABLE nodes DROP COLUMN IF EXISTS token;
        ALTER TABLE nodes DROP COLUMN IF EXISTS token_created_at;
        ALTER TABLE nodes DROP COLUMN IF EXISTS last_seen_server;
    END IF;
END $$;

-- 6. users（新增 uid；last_online_time -> last_login_at；删除登录 IP 列；download/upload_access 取反为 *_disabled；删除 terminal_access）
--    登录 IP 仅保留在 sessions.ip，不再落 users。
DO $$
BEGIN
    IF to_regclass('public.users') IS NOT NULL THEN
        ALTER TABLE users ADD COLUMN IF NOT EXISTS uid varchar(32);

        IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'users' AND column_name = 'last_online_time')
           AND NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'users' AND column_name = 'last_login_at') THEN
            ALTER TABLE users RENAME COLUMN last_online_time TO last_login_at;
        END IF;

        ALTER TABLE users ADD COLUMN IF NOT EXISTS download_disabled boolean NOT NULL DEFAULT FALSE;
        IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'users' AND column_name = 'download_access') THEN
            UPDATE users SET download_disabled = NOT COALESCE(download_access, TRUE);
            ALTER TABLE users DROP COLUMN download_access;
        END IF;

        ALTER TABLE users ADD COLUMN IF NOT EXISTS upload_disabled boolean NOT NULL DEFAULT FALSE;
        IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'users' AND column_name = 'upload_access') THEN
            UPDATE users SET upload_disabled = NOT COALESCE(upload_access, TRUE);
            ALTER TABLE users DROP COLUMN upload_access;
        END IF;

        ALTER TABLE users DROP COLUMN IF EXISTS terminal_access;
        ALTER TABLE users DROP COLUMN IF EXISTS last_online_ip;
        ALTER TABLE users DROP COLUMN IF EXISTS last_login_ip;
    END IF;
END $$;

-- 7. sessions（新增客户端自报元信息）
DO $$
BEGIN
    IF to_regclass('public.sessions') IS NOT NULL THEN
        ALTER TABLE sessions ADD COLUMN IF NOT EXISTS hostname varchar(255);
        ALTER TABLE sessions ADD COLUMN IF NOT EXISTS platform varchar(32);
        ALTER TABLE sessions ADD COLUMN IF NOT EXISTS os_version varchar(64);
        ALTER TABLE sessions ADD COLUMN IF NOT EXISTS app_version varchar(64);
        ALTER TABLE sessions ADD COLUMN IF NOT EXISTS device_id varchar(64);
    END IF;
END $$;

-- 8. connections（target_node_id -> node_id，display 转整型，status 3->2，删除 alias/shared/host/source/extra）
DO $$
DECLARE
    disp_type text;
BEGIN
    IF to_regclass('public.connections') IS NOT NULL THEN
        DROP INDEX IF EXISTS idx_conn_shared_node_protocol;
        DROP INDEX IF EXISTS idx_conn_node_user_port;
        DROP INDEX IF EXISTS idx_conn_node_display;
        DROP INDEX IF EXISTS idx_connections_shared;
        DROP INDEX IF EXISTS idx_connections_source;
        DROP INDEX IF EXISTS idx_connections_target_node_id;

        IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'connections' AND column_name = 'target_node_id')
           AND NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'connections' AND column_name = 'node_id') THEN
            ALTER TABLE connections RENAME COLUMN target_node_id TO node_id;
        END IF;

        ALTER TABLE connections ADD COLUMN IF NOT EXISTS resource_id bigint;

        IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'connections' AND column_name = 'extra') THEN
            UPDATE connections SET status = 2 WHERE status = 3;
        END IF;

        SELECT data_type INTO disp_type
        FROM information_schema.columns
        WHERE table_schema = 'public' AND table_name = 'connections' AND column_name = 'display';
        IF disp_type IN ('character varying', 'text', 'character') THEN
            ALTER TABLE connections
            ALTER COLUMN display TYPE integer
            USING CASE
                WHEN NULLIF(ltrim(trim(display), ':'), '') ~ '^[0-9]+$'
                THEN ltrim(trim(display), ':')::integer
                ELSE NULL
            END;
        END IF;

        UPDATE connections SET name = left(name, 64) WHERE char_length(name) > 64;
        ALTER TABLE connections ALTER COLUMN name TYPE varchar(64);
        UPDATE connections SET username = left(username, 64) WHERE char_length(username) > 64;
        ALTER TABLE connections ALTER COLUMN username TYPE varchar(64);
        UPDATE connections SET node_id = left(node_id, 64) WHERE char_length(node_id) > 64;
        ALTER TABLE connections ALTER COLUMN node_id TYPE varchar(64);

        ALTER TABLE connections DROP COLUMN IF EXISTS alias;
        ALTER TABLE connections DROP COLUMN IF EXISTS shared;
        ALTER TABLE connections DROP COLUMN IF EXISTS host;
        ALTER TABLE connections DROP COLUMN IF EXISTS source;
        ALTER TABLE connections DROP COLUMN IF EXISTS extra;
    END IF;
END $$;

-- 9. execution_records / license_services / task_leases
DO $$
BEGIN
    IF to_regclass('public.execution_records') IS NOT NULL THEN
        ALTER TABLE execution_records DROP COLUMN IF EXISTS success;
    END IF;
    IF to_regclass('public.license_services') IS NOT NULL THEN
        ALTER TABLE license_services ADD COLUMN IF NOT EXISTS lmutil_path varchar(256);
    END IF;
    IF to_regclass('public.task_leases') IS NOT NULL THEN
        ALTER TABLE task_leases ADD COLUMN IF NOT EXISTS epoch bigint NOT NULL DEFAULT 0;
    END IF;
END $$;

-- 10. ticket_projects & tickets
DO $$
BEGIN
    IF to_regclass('public.ticket_projects') IS NOT NULL THEN
        ALTER TABLE ticket_projects DROP CONSTRAINT IF EXISTS uni_ticket_projects_name;
        DROP INDEX IF EXISTS uni_ticket_projects_name;
        DROP INDEX IF EXISTS idx_ticket_projects_is_archived;

        IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'ticket_projects' AND column_name = 'is_archived')
           AND NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'ticket_projects' AND column_name = 'archived') THEN
            ALTER TABLE ticket_projects RENAME COLUMN is_archived TO archived;
        END IF;
        ALTER TABLE ticket_projects ADD COLUMN IF NOT EXISTS archived boolean NOT NULL DEFAULT FALSE;
    END IF;

    IF to_regclass('public.tickets') IS NOT NULL THEN
        ALTER TABLE tickets ADD COLUMN IF NOT EXISTS version integer NOT NULL DEFAULT 1;
        ALTER TABLE tickets ADD COLUMN IF NOT EXISTS completed_at timestamptz;
        ALTER TABLE tickets ALTER COLUMN assignee_id DROP NOT NULL;
        UPDATE tickets SET assignee_id = NULL WHERE assignee_id = 0;
        UPDATE tickets SET acceptance_criteria = NULL WHERE trim(acceptance_criteria) = '';
        UPDATE tickets SET attachments = NULL WHERE trim(attachments) = '';
    END IF;
END $$;

-- 11. cache_versions 清理旧主题（新版使用 settings/sessions/bans/identity/node_overrides）
DO $$
BEGIN
    IF to_regclass('public.cache_versions') IS NOT NULL THEN
        DELETE FROM cache_versions WHERE name IN ('session', 'ban', 'id_mappings');
    END IF;
END $$;

-- 12. settings 域重命名、废弃域清理（tunnel -> connection.proxy）与 JSON 键名/约束同步
DO $$
DECLARE
    tunnel_val jsonb;
    conn_val jsonb;
    proxy_obj jsonb;
    new_conn jsonb;
BEGIN
    IF to_regclass('public.settings') IS NOT NULL THEN
        -- 12.1 dashboard -> overview
        IF EXISTS (SELECT 1 FROM settings WHERE domain = 'dashboard') THEN
            IF NOT EXISTS (SELECT 1 FROM settings WHERE domain = 'overview') THEN
                UPDATE settings SET domain = 'overview' WHERE domain = 'dashboard';
            ELSE
                DELETE FROM settings WHERE domain = 'dashboard';
            END IF;
        END IF;

        UPDATE settings
        SET value = (value::jsonb - 'resourceRefreshInterval' || jsonb_build_object('metricsRefreshInterval', value::jsonb->'resourceRefreshInterval'))::text
        WHERE domain = 'overview'
          AND trim(value) ~ '^\{.*\}$'
          AND value::jsonb ? 'resourceRefreshInterval';

        UPDATE settings
        SET value = (value::jsonb - 'allowNormalUser' || jsonb_build_object('normalUserEnabled', value::jsonb->'allowNormalUser'))::text
        WHERE domain = 'overview'
          AND trim(value) ~ '^\{.*\}$'
          AND value::jsonb ? 'allowNormalUser';

        UPDATE settings
        SET value = (value::jsonb || '{"normalUserEnabled": true}'::jsonb)::text
        WHERE domain = 'overview'
          AND trim(value) ~ '^\{.*\}$'
          AND NOT (value::jsonb ? 'normalUserEnabled');

        -- 12.2 tunnel -> connection.proxy，并删除废弃的 tunnel 域
        SELECT value::jsonb INTO tunnel_val
        FROM settings
        WHERE domain = 'tunnel' AND trim(value) ~ '^\{.*\}$';

        SELECT value::jsonb INTO conn_val
        FROM settings
        WHERE domain = 'connection' AND trim(value) ~ '^\{.*\}$';

        IF tunnel_val IS NOT NULL OR (conn_val IS NOT NULL AND conn_val ?| ARRAY['maxConnectionsPerUser', 'connectionListCacheTTL']) THEN
            proxy_obj := jsonb_build_object(
                'enabled', COALESCE(tunnel_val->'enabled', conn_val->'proxy'->'enabled', 'true'::jsonb),
                'vncEnabled', COALESCE(tunnel_val->'vncEnabled', conn_val->'proxy'->'vncEnabled', 'true'::jsonb),
                'rdpEnabled', COALESCE(tunnel_val->'rdpEnabled', conn_val->'proxy'->'rdpEnabled', 'true'::jsonb),
                'sshEnabled', COALESCE(tunnel_val->'sshEnabled', conn_val->'proxy'->'sshEnabled', 'true'::jsonb),
                'maxProxyConnections', COALESCE(tunnel_val->'maxTunnels', conn_val->'proxy'->'maxProxyConnections', '200'::jsonb),
                'maxProxyConnectionsPerUser', COALESCE(tunnel_val->'maxTunnelsPerUser', conn_val->'proxy'->'maxProxyConnectionsPerUser', '3'::jsonb)
            );
            new_conn := jsonb_build_object(
                'maxPerUserCap', COALESCE(conn_val->'maxPerUserCap', '16'::jsonb),
                'defaultMaxConnectionsPerNode', COALESCE(conn_val->'defaultMaxConnectionsPerNode', '0'::jsonb),
                'proxy', proxy_obj
            );
            INSERT INTO settings (domain, value)
            VALUES ('connection', new_conn::text)
            ON CONFLICT (domain) DO UPDATE SET value = EXCLUDED.value;
        END IF;
        DELETE FROM settings WHERE domain = 'tunnel';

        -- 12.3 platform: website -> pages, disabledModules array -> map
        UPDATE settings
        SET value = (
            value::jsonb - 'website' || jsonb_build_object(
                'pages', jsonb_build_object(
                    'index', COALESCE(value::jsonb->'website'->'indexEnabled', 'true'::jsonb),
                    'overview', COALESCE(value::jsonb->'website'->'dashboardEnabled', 'true'::jsonb)
                )
            )
        )::text
        WHERE domain = 'platform'
          AND trim(value) ~ '^\{.*\}$'
          AND value::jsonb ? 'website'
          AND NOT (value::jsonb ? 'pages');

        UPDATE settings
        SET value = (value::jsonb - 'website')::text
        WHERE domain = 'platform'
          AND trim(value) ~ '^\{.*\}$'
          AND value::jsonb ? 'website';

        UPDATE settings
        SET value = jsonb_set(
            value::jsonb,
            '{disabledModules}',
            jsonb_build_object(
                'ai', (value::jsonb->'disabledModules') ? 'ai',
                'app', (value::jsonb->'disabledModules') ? 'app',
                'connection', (value::jsonb->'disabledModules') ? 'connection',
                'file', (value::jsonb->'disabledModules') ? 'file',
                'license', (value::jsonb->'disabledModules') ? 'license',
                'overview', ((value::jsonb->'disabledModules') ? 'overview') OR ((value::jsonb->'disabledModules') ? 'dashboard'),
                'scheduler', (value::jsonb->'disabledModules') ? 'scheduler',
                'ticket', (value::jsonb->'disabledModules') ? 'ticket'
            )
        )::text
        WHERE domain = 'platform'
          AND trim(value) ~ '^\{.*\}$'
          AND jsonb_typeof(value::jsonb->'disabledModules') = 'array';

        -- 12.4 file: requiresApproval -> approvalRequired，下限对齐
        UPDATE settings
        SET value = (value::jsonb - 'requiresApproval' || jsonb_build_object('approvalRequired', value::jsonb->'requiresApproval'))::text
        WHERE domain = 'file'
          AND trim(value) ~ '^\{.*\}$'
          AND value::jsonb ? 'requiresApproval';

        UPDATE settings
        SET value = jsonb_set(value::jsonb, '{downloadApprovalTTL}', '3600'::jsonb)::text
        WHERE domain = 'file'
          AND trim(value) ~ '^\{.*\}$'
          AND value::jsonb ? 'downloadApprovalTTL'
          AND (value::jsonb->>'downloadApprovalTTL')::bigint < 3600;

        -- 12.5 auth: maxLoginSessionsPerUser -> maxSessionsPerUser 与阈值对齐
        UPDATE settings
        SET value = (value::jsonb - 'maxLoginSessionsPerUser' || jsonb_build_object('maxSessionsPerUser', value::jsonb->'maxLoginSessionsPerUser'))::text
        WHERE domain = 'auth'
          AND trim(value) ~ '^\{.*\}$'
          AND value::jsonb ? 'maxLoginSessionsPerUser';

        UPDATE settings
        SET value = jsonb_set(
            value::jsonb,
            '{maxSessionsPerUser}',
            to_jsonb(LEAST(5, GREATEST(1, COALESCE((value::jsonb->>'maxSessionsPerUser')::int, 1))))
        )::text
        WHERE domain = 'auth'
          AND trim(value) ~ '^\{.*\}$'
          AND value::jsonb ? 'maxSessionsPerUser'
          AND ((value::jsonb->>'maxSessionsPerUser')::int < 1 OR (value::jsonb->>'maxSessionsPerUser')::int > 5);

        UPDATE settings
        SET value = jsonb_set(value::jsonb, '{sessionCleanupInterval}', '1800'::jsonb)::text
        WHERE domain = 'auth'
          AND trim(value) ~ '^\{.*\}$'
          AND value::jsonb ? 'sessionCleanupInterval'
          AND ((value::jsonb->>'sessionCleanupInterval')::int < 60 OR (value::jsonb->>'sessionCleanupInterval')::int > 86400);

        UPDATE settings
        SET value = jsonb_set(
            value::jsonb,
            '{maxUserLoginAttempts}',
            to_jsonb(LEAST(10, GREATEST(5, (value::jsonb->>'maxUserLoginAttempts')::int)))
        )::text
        WHERE domain = 'auth'
          AND trim(value) ~ '^\{.*\}$'
          AND value::jsonb ? 'maxUserLoginAttempts'
          AND ((value::jsonb->>'maxUserLoginAttempts')::int < 5 OR (value::jsonb->>'maxUserLoginAttempts')::int > 10);

        UPDATE settings
        SET value = jsonb_set(
            value::jsonb,
            '{maxIPLoginAttempts}',
            to_jsonb(LEAST(100, GREATEST(10, (value::jsonb->>'maxIPLoginAttempts')::int)))
        )::text
        WHERE domain = 'auth'
          AND trim(value) ~ '^\{.*\}$'
          AND value::jsonb ? 'maxIPLoginAttempts'
          AND ((value::jsonb->>'maxIPLoginAttempts')::int < 10 OR (value::jsonb->>'maxIPLoginAttempts')::int > 100);

        -- 12.6 agent: launcherPath -> execBinPath, resourceReport* -> metrics*, useCgroup -> cgroupEnabled
        UPDATE settings
        SET value = (value::jsonb - 'launcherPath' || jsonb_build_object('execBinPath', value::jsonb->'launcherPath'))::text
        WHERE domain = 'agent'
          AND trim(value) ~ '^\{.*\}$'
          AND value::jsonb ? 'launcherPath';

        UPDATE settings
        SET value = (value::jsonb || '{"logLevel":"INFO"}'::jsonb)::text
        WHERE domain = 'agent'
          AND trim(value) ~ '^\{.*\}$'
          AND NOT (value::jsonb ? 'logLevel');

        UPDATE settings
        SET value = jsonb_set(
            value::jsonb #- '{vnc,useCgroup}',
            '{vnc,cgroupEnabled}',
            value::jsonb #> '{vnc,useCgroup}'
        )::text
        WHERE domain = 'agent'
          AND trim(value) ~ '^\{.*\}$'
          AND (value::jsonb #> '{vnc}') ? 'useCgroup';

        UPDATE settings
        SET value = jsonb_set(value::jsonb, '{vnc,scanInterval}', '60'::jsonb)::text
        WHERE domain = 'agent'
          AND trim(value) ~ '^\{.*\}$'
          AND (value::jsonb #> '{vnc}') ? 'scanInterval'
          AND ((value::jsonb #> '{vnc}' ->>'scanInterval')::int < 30 OR (value::jsonb #> '{vnc}' ->>'scanInterval')::int > 86400);

        UPDATE settings
        SET value = jsonb_set(
            value::jsonb #- '{monitor,resourceReportEnabled}',
            '{monitor,metricsEnabled}',
            value::jsonb #> '{monitor,resourceReportEnabled}'
        )::text
        WHERE domain = 'agent'
          AND trim(value) ~ '^\{.*\}$'
          AND (value::jsonb #> '{monitor}') ? 'resourceReportEnabled';

        UPDATE settings
        SET value = jsonb_set(
            value::jsonb #- '{monitor,resourceReportInterval}',
            '{monitor,metricsInterval}',
            value::jsonb #> '{monitor,resourceReportInterval}'
        )::text
        WHERE domain = 'agent'
          AND trim(value) ~ '^\{.*\}$'
          AND (value::jsonb #> '{monitor}') ? 'resourceReportInterval';

        UPDATE settings
        SET value = jsonb_set(value::jsonb, '{monitor,metricsInterval}', '60'::jsonb)::text
        WHERE domain = 'agent'
          AND trim(value) ~ '^\{.*\}$'
          AND (value::jsonb #> '{monitor}') ? 'metricsInterval'
          AND ((value::jsonb #> '{monitor}' ->>'metricsInterval')::int < 30 OR (value::jsonb #> '{monitor}' ->>'metricsInterval')::int > 3600);

        -- 12.7 scheduler: 补齐默认 type='lsf'
        UPDATE settings
        SET value = (value::jsonb || '{"type":"lsf"}'::jsonb)::text
        WHERE domain = 'scheduler'
          AND trim(value) ~ '^\{.*\}$'
          AND NOT (value::jsonb ? 'type');

        -- 12.8 alert / license 阈值下限对齐
        UPDATE settings
        SET value = jsonb_set(value::jsonb, '{cpu,maxUsedPercent}', '90'::jsonb)::text
        WHERE domain = 'alert'
          AND trim(value) ~ '^\{.*\}$'
          AND (value::jsonb #> '{cpu}') ? 'maxUsedPercent'
          AND ((value::jsonb #> '{cpu}' ->>'maxUsedPercent')::double precision < 1 OR (value::jsonb #> '{cpu}' ->>'maxUsedPercent')::double precision > 100);

        UPDATE settings
        SET value = jsonb_set(value::jsonb, '{load,maxLoadPercent}', '110'::jsonb)::text
        WHERE domain = 'alert'
          AND trim(value) ~ '^\{.*\}$'
          AND (value::jsonb #> '{load}') ? 'maxLoadPercent'
          AND ((value::jsonb #> '{load}' ->>'maxLoadPercent')::double precision < 50 OR (value::jsonb #> '{load}' ->>'maxLoadPercent')::double precision > 200);

        UPDATE settings
        SET value = jsonb_set(value::jsonb, '{statusCacheTTL}', '60'::jsonb)::text
        WHERE domain = 'license'
          AND trim(value) ~ '^\{.*\}$'
          AND value::jsonb ? 'statusCacheTTL'
          AND (value::jsonb->>'statusCacheTTL')::int < 60;

        UPDATE settings
        SET value = jsonb_set(value::jsonb, '{featuresCacheTTL}', '60'::jsonb)::text
        WHERE domain = 'license'
          AND trim(value) ~ '^\{.*\}$'
          AND value::jsonb ? 'featuresCacheTTL'
          AND (value::jsonb->>'featuresCacheTTL')::int < 60;
    END IF;
END $$;

-- 13. 0.2.x model 对齐：新增表、缺列与新索引（幂等）
DO $$
BEGIN
    IF to_regclass('public.nodes') IS NOT NULL THEN
        ALTER TABLE nodes ADD COLUMN IF NOT EXISTS os varchar(32);
        ALTER TABLE nodes ADD COLUMN IF NOT EXISTS is_server boolean NOT NULL DEFAULT FALSE;
        ALTER TABLE nodes ADD COLUMN IF NOT EXISTS cert_fingerprint_prev varchar(128);
        ALTER TABLE nodes ADD COLUMN IF NOT EXISTS max_connections bigint NOT NULL DEFAULT 0;
        ALTER TABLE nodes ADD COLUMN IF NOT EXISTS cert_alert_stage smallint NOT NULL DEFAULT 0;
    END IF;

    IF to_regclass('public.apps') IS NOT NULL THEN
        ALTER TABLE apps ADD COLUMN IF NOT EXISTS description varchar(512);
    END IF;
END $$;

CREATE TABLE IF NOT EXISTS connection_resources (
    id           bigserial PRIMARY KEY,
    slug         varchar(32)  NOT NULL,
    name         varchar(32)  NOT NULL DEFAULT '',
    description  varchar(256),
    protocol     bigint       NOT NULL,
    nodes        jsonb        NOT NULL DEFAULT '[]'::jsonb,
    node_groups  jsonb        NOT NULL DEFAULT '[]'::jsonb,
    display      bigint,
    port         bigint,
    max_per_user bigint       NOT NULL DEFAULT 1,
    max_total    bigint       NOT NULL DEFAULT 0,
    vnc_config   jsonb,
    public       boolean      NOT NULL DEFAULT FALSE,
    created_at   timestamptz,
    updated_at   timestamptz
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_connection_resources_slug ON connection_resources (slug);

CREATE TABLE IF NOT EXISTS user_connection_resources (
    username    varchar(32) NOT NULL,
    resource_id bigint      NOT NULL,
    PRIMARY KEY (username, resource_id)
);
CREATE INDEX IF NOT EXISTS idx_ucr_resource_id ON user_connection_resources (resource_id);

CREATE TABLE IF NOT EXISTS ticket_comments (
    id         bigserial PRIMARY KEY,
    ticket_id  bigint    NOT NULL,
    author_id  bigint    NOT NULL,
    body       text      NOT NULL,
    created_at timestamptz,
    updated_at timestamptz
);
CREATE INDEX IF NOT EXISTS idx_ticket_comments_ticket ON ticket_comments (ticket_id);

CREATE TABLE IF NOT EXISTS ticket_events (
    id         bigserial PRIMARY KEY,
    ticket_id  bigint       NOT NULL,
    actor_id   bigint       NOT NULL,
    action     varchar(32)  NOT NULL,
    field      varchar(64),
    from_value text,
    to_value   text,
    created_at timestamptz
);
CREATE INDEX IF NOT EXISTS idx_ticket_events_ticket ON ticket_events (ticket_id);

DO $$
BEGIN
    IF to_regclass('public.connections') IS NOT NULL THEN
        ALTER TABLE connections ADD COLUMN IF NOT EXISTS resource_id bigint;
        ALTER TABLE connections ADD COLUMN IF NOT EXISTS last_active_at timestamptz;

        -- last_accessed_at / controlled_at → last_active_at（统一活跃时间）
        IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'connections' AND column_name = 'last_accessed_at')
           AND EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'connections' AND column_name = 'controlled_at') THEN
            UPDATE connections
            SET last_active_at = COALESCE(last_active_at, last_accessed_at, controlled_at);
        ELSIF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'connections' AND column_name = 'last_accessed_at') THEN
            UPDATE connections
            SET last_active_at = COALESCE(last_active_at, last_accessed_at);
        ELSIF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'connections' AND column_name = 'controlled_at') THEN
            UPDATE connections
            SET last_active_at = COALESCE(last_active_at, controlled_at);
        END IF;

        ALTER TABLE connections DROP COLUMN IF EXISTS last_accessed_at;
        ALTER TABLE connections DROP COLUMN IF EXISTS controlled_at;

        CREATE INDEX IF NOT EXISTS idx_connections_resource_id ON connections (resource_id);
        CREATE INDEX IF NOT EXISTS idx_connections_last_active_at ON connections (last_active_at);
        CREATE UNIQUE INDEX IF NOT EXISTS idx_conn_node_user_port ON connections (node_id, username, port);
        CREATE UNIQUE INDEX IF NOT EXISTS idx_conn_node_display ON connections (node_id, display);
        CREATE INDEX IF NOT EXISTS idx_conn_status_node_id ON connections (status, node_id);
    END IF;

    -- user_connection_resources：存量 (user_id, resource_id) → (username, resource_id)；无 created_at
    IF to_regclass('public.user_connection_resources') IS NOT NULL THEN
        IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'user_connection_resources' AND column_name = 'user_id') THEN
            ALTER TABLE user_connection_resources ADD COLUMN IF NOT EXISTS username varchar(32);

            UPDATE user_connection_resources ucr
            SET username = u.username
            FROM users u
            WHERE u.id = ucr.user_id
              AND (ucr.username IS NULL OR ucr.username = '');

            DELETE FROM user_connection_resources WHERE username IS NULL OR username = '';

            ALTER TABLE user_connection_resources DROP CONSTRAINT IF EXISTS user_connection_resources_pkey;
            ALTER TABLE user_connection_resources DROP COLUMN user_id;
            ALTER TABLE user_connection_resources ALTER COLUMN username SET NOT NULL;
            ALTER TABLE user_connection_resources ADD PRIMARY KEY (username, resource_id);
        END IF;
        ALTER TABLE user_connection_resources DROP COLUMN IF EXISTS created_at;
    END IF;

    -- user_apps：存量 (user_id, app_id) → (username, app_id)；无 created_at
    IF to_regclass('public.user_apps') IS NOT NULL THEN
        IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'user_apps' AND column_name = 'user_id') THEN
            ALTER TABLE user_apps ADD COLUMN IF NOT EXISTS username varchar(32);

            UPDATE user_apps ua
            SET username = u.username
            FROM users u
            WHERE u.id = ua.user_id
              AND (ua.username IS NULL OR ua.username = '');

            DELETE FROM user_apps WHERE username IS NULL OR username = '';

            ALTER TABLE user_apps DROP CONSTRAINT IF EXISTS user_apps_pkey;
            ALTER TABLE user_apps DROP COLUMN user_id;
            ALTER TABLE user_apps ALTER COLUMN username SET NOT NULL;
            ALTER TABLE user_apps ADD PRIMARY KEY (username, app_id);
        END IF;
        ALTER TABLE user_apps DROP COLUMN IF EXISTS created_at;
        CREATE INDEX IF NOT EXISTS idx_user_apps_app_id ON user_apps (app_id);
    END IF;

    IF to_regclass('public.ticket_projects') IS NOT NULL THEN
        DROP INDEX IF EXISTS uni_ticket_projects_name;
        CREATE UNIQUE INDEX IF NOT EXISTS idx_ticket_projects_name_active
            ON ticket_projects (name) WHERE deleted_at IS NULL;
    END IF;

    IF to_regclass('public.tickets') IS NOT NULL THEN
        ALTER TABLE tickets ADD COLUMN IF NOT EXISTS version integer NOT NULL DEFAULT 1;
        ALTER TABLE tickets ADD COLUMN IF NOT EXISTS completed_at timestamptz;
        CREATE INDEX IF NOT EXISTS idx_tickets_completed_at ON tickets (completed_at);
    END IF;

    IF to_regclass('public.alert_histories') IS NULL THEN
        CREATE TABLE alert_histories (
            id          bigserial PRIMARY KEY,
            rule_key    varchar(128) NOT NULL,
            metric      double precision,
            threshold   double precision,
            alert_state varchar(32)  NOT NULL,
            level       varchar(32)  NOT NULL,
            message     text,
            created_at  timestamptz  NOT NULL
        );
        CREATE INDEX IF NOT EXISTS idx_alert_histories_rule_key ON alert_histories (rule_key);
        CREATE INDEX IF NOT EXISTS idx_alert_histories_alert_state ON alert_histories (alert_state);
        CREATE INDEX IF NOT EXISTS idx_alert_histories_created_at ON alert_histories (created_at);
    END IF;
END $$;

COMMIT;
