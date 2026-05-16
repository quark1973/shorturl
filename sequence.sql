CREATE TABLE `sequence` (
    `biz_tag` varchar(128) NOT NULL COMMENT 'business key',
    `max_id` bigint(20) unsigned NOT NULL DEFAULT '0' COMMENT 'allocated max id',
    `step` int(10) unsigned NOT NULL DEFAULT '1000' COMMENT 'segment size',
    `description` varchar(256) NOT NULL DEFAULT '' COMMENT 'description',
    `update_time` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (`biz_tag`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='leaf segment sequence table';

INSERT INTO `sequence` (`biz_tag`, `max_id`, `step`, `description`)
VALUES ('short_url', 0, 1000, 'short url id segment')
ON DUPLICATE KEY UPDATE `step` = VALUES(`step`);
