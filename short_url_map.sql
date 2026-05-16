CREATE TABLE `short_url_map` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '主键',
    `create_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
    `create_by` varchar(64) NOT NULL DEFAULT '' COMMENT '创建人',
    `is_del` tinyint unsigned NOT NULL DEFAULT '0' COMMENT '是否删除: 0 正常, 1 删除',
    `lurl` varchar(2048) DEFAULT NULL COMMENT '长链接',
    `md5` char(32) DEFAULT NULL COMMENT '长链接 MD5',
    `surl` varchar(11) DEFAULT NULL COMMENT '短链接码',
    PRIMARY KEY (`id`),
    KEY `idx_is_del` (`is_del`),
    UNIQUE KEY `uniq_md5` (`md5`),
    UNIQUE KEY `uniq_surl` (`surl`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='长短链映射表';
