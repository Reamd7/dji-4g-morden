import React from 'react';
import { Flex, Text } from '@radix-ui/themes';

export interface InfoRowProps {
  label: string;
  value: string;
}

/**
 * 信息行 — 左 label(灰)/ 右 value(高对比灰),两端对齐。
 * 设备详情、统计卡片、设置项等通用 label-value 展示。
 * 基于 Radix Flex + Text(props token 级别,无硬编码)。
 */
export const InfoRow: React.FC<InfoRowProps> = ({ label, value }) => (
  <Flex justify="between" align="center">
    <Text as="span" size="2" color="gray">{label}</Text>
    <Text as="span" size="2" color="gray" highContrast>{value}</Text>
  </Flex>
);
