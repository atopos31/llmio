-- 上游（master 分支）的库结构，不是手写的：由 master 的 models.Init 在一个空库上
-- 建出来后，把 sqlite_master 里的语句原样导出，一行一条。
--
-- 用途：models/migrate_test.go 用它搭一个"升级前"的库，验证新代码的 AutoMigrate
-- 能在这个库上就地升级。手写 DDL 只能证明"按我猜的旧结构能升级"，抄下来才能证明
-- "按上游真实建出来的旧结构能升级"。
--
-- 若上游改了表结构，重新导出即可（建空库 → 导出 sqlite_master）。

CREATE TABLE `auth_keys` (`id` integer PRIMARY KEY AUTOINCREMENT,`created_at` datetime,`updated_at` datetime,`deleted_at` datetime,`name` text,`key` text,`status` numeric,`io_log` numeric,`allow_all` numeric,`models` text,`expires_at` datetime,`usage_count` integer,`last_used_at` datetime);
CREATE TABLE `chat_ios` (`id` integer PRIMARY KEY AUTOINCREMENT,`created_at` datetime,`updated_at` datetime,`deleted_at` datetime,`log_id` integer,`input` text,`of_string` text,`of_string_array` text);
CREATE TABLE `chat_logs` (`id` integer PRIMARY KEY AUTOINCREMENT,`created_at` datetime,`updated_at` datetime,`deleted_at` datetime,`name` text,`trace_id` text,`provider_model` text,`provider_name` text,`status` text,`style` text,`user_agent` text,`remote_ip` text,`auth_key_id` integer,`session_id` text,`chat_io` numeric,`error` text,`retry` integer,`proxy_time` integer,`first_chunk_time` integer,`chunk_time` integer,`tps` real,`size` integer,`prompt_tokens` integer,`completion_tokens` integer,`total_tokens` integer,`prompt_tokens_details` text,`input_price` real,`cache_read_price` real,`output_price` real,`currency` text);
CREATE TABLE `configs` (`id` integer PRIMARY KEY AUTOINCREMENT,`created_at` datetime,`updated_at` datetime,`deleted_at` datetime,`key` text,`value` text);
CREATE TABLE `log_cleanup_records` (`id` integer PRIMARY KEY AUTOINCREMENT,`created_at` datetime,`updated_at` datetime,`deleted_at` datetime,`retention_days` integer,`deleted_count` integer,`duration_ms` integer,`source` text,`type` text);
CREATE TABLE `model_with_providers` (`id` integer PRIMARY KEY AUTOINCREMENT,`created_at` datetime,`updated_at` datetime,`deleted_at` datetime,`model_id` integer,`provider_model` text,`provider_id` integer,`tool_call` numeric,`structured_output` numeric,`image` numeric,`with_header` numeric,`status` numeric,`customer_headers` text,`extra_body` text,`weight` integer,`input_price` real,`cache_read_price` real,`output_price` real,`currency` text);
CREATE TABLE `models` (`id` integer PRIMARY KEY AUTOINCREMENT,`created_at` datetime,`updated_at` datetime,`deleted_at` datetime,`name` text,`remark` text,`max_retry` integer,`time_out` integer,`strategy` text,`breaker` numeric,`display_order` integer);
CREATE TABLE `providers` (`id` integer PRIMARY KEY AUTOINCREMENT,`created_at` datetime,`updated_at` datetime,`deleted_at` datetime,`name` text,`type` text,`config` text,`console` text,`proxy` text,`error_matcher` text);
CREATE INDEX `idx_auth_keys_deleted_at` ON `auth_keys`(`deleted_at`);
CREATE INDEX `idx_chat_ios_deleted_at` ON `chat_ios`(`deleted_at`);
CREATE INDEX `idx_chat_logs_auth_key_id` ON `chat_logs`(`auth_key_id`);
CREATE INDEX `idx_chat_logs_deleted_at` ON `chat_logs`(`deleted_at`);
CREATE INDEX `idx_chat_logs_name` ON `chat_logs`(`name`);
CREATE INDEX `idx_chat_logs_provider_model` ON `chat_logs`(`provider_model`);
CREATE INDEX `idx_chat_logs_provider_name` ON `chat_logs`(`provider_name`);
CREATE INDEX `idx_chat_logs_session_id` ON `chat_logs`(`session_id`);
CREATE INDEX `idx_chat_logs_status` ON `chat_logs`(`status`);
CREATE INDEX `idx_chat_logs_trace_id` ON `chat_logs`(`trace_id`);
CREATE INDEX `idx_chat_logs_user_agent` ON `chat_logs`(`user_agent`);
CREATE INDEX `idx_configs_deleted_at` ON `configs`(`deleted_at`);
CREATE INDEX `idx_log_cleanup_records_deleted_at` ON `log_cleanup_records`(`deleted_at`);
CREATE INDEX `idx_model_with_providers_deleted_at` ON `model_with_providers`(`deleted_at`);
CREATE INDEX `idx_models_deleted_at` ON `models`(`deleted_at`);
CREATE INDEX `idx_providers_deleted_at` ON `providers`(`deleted_at`);
