import React from 'react';
import { Card, Text, Flex, Badge, Separator } from '@radix-ui/themes';

/**
 * 设备信息卡 — 业务复合组件示例。
 *
 * 演示设计规范的「封装」模式:组件内部完全用 Radix Themes 原语
 * (Card/Flex/Text/Badge,props 是 token 级别 size/color/gap),
 * 不含任何硬编码颜色或间距。业务页面只需 <DeviceCard {...} />,
 * 视觉一致性由组件保证。
 */
export interface DeviceCardProps {
  model: string;
  imei: string;
  iccid: string;
  carrier: string;
  signalRssi?: number; // AT+CSQ 0-31, 99=未知
  connected?: boolean;
}

const InfoRow: React.FC<{ label: string; value: string }> = ({ label, value }) => (
  <Flex justify="between" align="center">
    <Text as="span" size="2" color="gray">{label}</Text>
    <Text as="span" size="2" color="gray" highContrast>{value}</Text>
  </Flex>
);

export const DeviceCard: React.FC<DeviceCardProps> = ({
  model,
  imei,
  iccid,
  carrier,
  signalRssi,
  connected = false,
}) => {
  const signalText =
    signalRssi === undefined ? '—'
    : signalRssi === 99 ? '无信号'
    : `${signalRssi} · ${-113 + signalRssi * 2} dBm`;

  return (
    <Card size="2">
      <Flex direction="column" gap="3">
        <Flex justify="between" align="center">
          <Text as="span" size="5" weight="bold">{model}</Text>
          <Badge color={connected ? 'green' : 'gray'} variant="soft" size="2">
            {connected ? '已连接' : '未连接'}
          </Badge>
        </Flex>
        <Separator size="4" />
        <Flex direction="column" gap="2">
          <InfoRow label="IMEI" value={imei} />
          <InfoRow label="ICCID" value={iccid} />
          <InfoRow label="运营商" value={carrier} />
          <InfoRow label="信号" value={signalText} />
        </Flex>
      </Flex>
    </Card>
  );
};
