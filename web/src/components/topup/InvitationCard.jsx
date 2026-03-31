/*
Copyright (C) 2025 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/

import React, { useState } from 'react';
import {
  Avatar,
  Typography,
  Card,
  Button,
  Input,
  Badge,
  Space,
  Table,
  Pagination,
  Tag,
} from '@douyinfe/semi-ui';
import {
  Copy,
  Users,
  BarChart2,
  TrendingUp,
  Gift,
  Zap,
  UserPlus,
  UserCheck,
  List,
} from 'lucide-react';

const { Text } = Typography;

const InvitationCard = ({
  t,
  userState,
  renderQuota,
  setOpenTransfer,
  affLink,
  handleAffLinkClick,
  inviterInfo,
  onBindInviter,
  bindLoading,
  invitees,
  inviteesTotal,
  onInviteesPageChange,
  rebateConfig,
}) => {
  const rc = rebateConfig || {};
  const [bindCode, setBindCode] = useState('');
  const hasInviter =
    userState?.user?.inviter_id > 0 || inviterInfo != null;

  return (
    <Card className='!rounded-2xl shadow-sm border-0'>
      {/* 卡片头部 */}
      <div className='flex items-center mb-4'>
        <Avatar size='small' color='green' className='mr-3 shadow-md'>
          <Gift size={16} />
        </Avatar>
        <div>
          <Typography.Text className='text-lg font-medium'>
            {t('邀请奖励')}
          </Typography.Text>
          <div className='text-xs'>{t('邀请好友获得额外奖励')}</div>
        </div>
      </div>

      {/* 收益展示区域 */}
      <Space vertical style={{ width: '100%' }}>
        {/* 统计数据统一卡片 */}
        <Card
          className='!rounded-xl w-full'
          cover={
            <div
              className='relative h-30'
              style={{
                '--palette-primary-darkerChannel': '0 75 80',
                backgroundImage: `linear-gradient(0deg, rgba(var(--palette-primary-darkerChannel) / 80%), rgba(var(--palette-primary-darkerChannel) / 80%)), url('/cover-4.webp')`,
                backgroundSize: 'cover',
                backgroundPosition: 'center',
                backgroundRepeat: 'no-repeat',
              }}
            >
              {/* 标题和按钮 */}
              <div className='relative z-10 h-full flex flex-col justify-between p-4'>
                <div className='flex justify-between items-center'>
                  <Text strong style={{ color: 'white', fontSize: '16px' }}>
                    {t('收益统计')}
                  </Text>
                  <Button
                    type='primary'
                    theme='solid'
                    size='small'
                    disabled={
                      !userState?.user?.aff_quota ||
                      userState?.user?.aff_quota <= 0
                    }
                    onClick={() => setOpenTransfer(true)}
                    className='!rounded-lg'
                  >
                    <Zap size={12} className='mr-1' />
                    {t('划转到余额')}
                  </Button>
                </div>

                {/* 统计数据 */}
                <div className='grid grid-cols-3 gap-6 mt-4'>
                  {/* 待使用收益 */}
                  <div className='text-center'>
                    <div
                      className='text-base sm:text-2xl font-bold mb-2'
                      style={{ color: 'white' }}
                    >
                      {renderQuota(userState?.user?.aff_quota || 0)}
                    </div>
                    <div className='flex items-center justify-center text-sm'>
                      <TrendingUp
                        size={14}
                        className='mr-1'
                        style={{ color: 'rgba(255,255,255,0.8)' }}
                      />
                      <Text
                        style={{
                          color: 'rgba(255,255,255,0.8)',
                          fontSize: '12px',
                        }}
                      >
                        {t('待使用收益')}
                      </Text>
                    </div>
                  </div>

                  {/* 总收益 */}
                  <div className='text-center'>
                    <div
                      className='text-base sm:text-2xl font-bold mb-2'
                      style={{ color: 'white' }}
                    >
                      {renderQuota(userState?.user?.aff_history_quota || 0)}
                    </div>
                    <div className='flex items-center justify-center text-sm'>
                      <BarChart2
                        size={14}
                        className='mr-1'
                        style={{ color: 'rgba(255,255,255,0.8)' }}
                      />
                      <Text
                        style={{
                          color: 'rgba(255,255,255,0.8)',
                          fontSize: '12px',
                        }}
                      >
                        {t('总收益')}
                      </Text>
                    </div>
                  </div>

                  {/* 邀请人数 */}
                  <div className='text-center'>
                    <div
                      className='text-base sm:text-2xl font-bold mb-2'
                      style={{ color: 'white' }}
                    >
                      {userState?.user?.aff_count || 0}
                    </div>
                    <div className='flex items-center justify-center text-sm'>
                      <Users
                        size={14}
                        className='mr-1'
                        style={{ color: 'rgba(255,255,255,0.8)' }}
                      />
                      <Text
                        style={{
                          color: 'rgba(255,255,255,0.8)',
                          fontSize: '12px',
                        }}
                      >
                        {t('邀请人数')}
                      </Text>
                    </div>
                  </div>
                </div>
              </div>
            </div>
          }
        >
          {/* 邀请链接部分 */}
          <Input
            value={affLink}
            readonly
            className='!rounded-lg'
            prefix={t('邀请链接')}
            suffix={
              <Button
                type='primary'
                theme='solid'
                onClick={handleAffLinkClick}
                icon={<Copy size={14} />}
                className='!rounded-lg'
              >
                {t('复制')}
              </Button>
            }
          />
        </Card>

        {/* 我的邀请人 */}
        <Card className='!rounded-xl w-full'>
          <div className='flex items-center mb-3'>
            <UserCheck size={16} className='mr-2 text-blue-500' />
            <Text strong>{t('我的邀请人')}</Text>
          </div>
          {hasInviter ? (
            <div className='flex items-center gap-2'>
              <Tag color='blue' size='large'>
                {inviterInfo?.display_name || inviterInfo?.username || `ID: ${userState?.user?.inviter_id}`}
              </Tag>
            </div>
          ) : (
            <div className='flex items-center gap-2'>
              <Input
                placeholder={t('请输入邀请码')}
                value={bindCode}
                onChange={setBindCode}
                className='!rounded-lg'
                style={{ flex: 1 }}
              />
              <Button
                type='primary'
                theme='solid'
                loading={bindLoading}
                disabled={!bindCode}
                onClick={() => {
                  onBindInviter(bindCode);
                  setBindCode('');
                }}
                icon={<UserPlus size={14} />}
                className='!rounded-lg'
              >
                {t('绑定')}
              </Button>
            </div>
          )}
        </Card>

        {/* 邀请列表 */}
        {inviteesTotal > 0 && (
          <Card className='!rounded-xl w-full'>
            <div className='flex items-center justify-between mb-3'>
              <div className='flex items-center'>
                <List size={16} className='mr-2 text-green-500' />
                <Text strong>{t('我邀请的用户')}</Text>
              </div>
              <Tag color='green'>{inviteesTotal} {t('人')}</Tag>
            </div>
            <div className='space-y-2'>
              {invitees.map((item) => (
                <div
                  key={item.id}
                  className='flex items-center justify-between py-1 px-2 rounded-lg'
                  style={{ background: 'var(--semi-color-fill-0)' }}
                >
                  <Text size='small'>
                    {item.display_name || item.username}
                  </Text>
                  <Text size='small' type='tertiary'>
                    ID: {item.id}
                  </Text>
                </div>
              ))}
            </div>
            {inviteesTotal > 10 && (
              <div className='flex justify-center mt-3'>
                <Pagination
                  total={inviteesTotal}
                  pageSize={10}
                  onChange={onInviteesPageChange}
                  size='small'
                />
              </div>
            )}
          </Card>
        )}

        {/* 奖励说明 */}
        <Card className='!rounded-xl w-full'>
          <div className='flex items-center mb-3'>
            <Gift size={16} className='mr-2 text-amber-500' />
            <Text strong>{t('奖励说明')}</Text>
          </div>
          <div className='space-y-3'>
            {rc.quotaForInviter > 0 && (
              <div className='flex items-start gap-2'>
                <Badge dot type='success' />
                <Text className='text-sm'>
                  {t('每成功邀请一位好友注册，您将获得')}{' '}
                  <Text strong type='success'>{renderQuota(rc.quotaForInviter)}</Text>{' '}
                  {t('奖励')}
                </Text>
              </div>
            )}
            {rc.quotaForInvitee > 0 && (
              <div className='flex items-start gap-2'>
                <Badge dot type='success' />
                <Text className='text-sm'>
                  {t('新用户通过邀请码注册可获得')}{' '}
                  <Text strong type='success'>{renderQuota(rc.quotaForInvitee)}</Text>{' '}
                  {t('额度奖励')}
                </Text>
              </div>
            )}
            {rc.rebateEnabled && (
              <>
                {rc.rebateMode === 'ratio' ? (
                  <>
                    {rc.rebateRatio > 0 && (
                      <div className='flex items-start gap-2'>
                        <Badge dot type='warning' />
                        <Text className='text-sm'>
                          {t('好友每次充值，您可获得充值额度的')}{' '}
                          <Text strong type='warning'>{(rc.rebateRatio * 100).toFixed(0)}%</Text>{' '}
                          {t('作为返利奖励')}
                        </Text>
                      </div>
                    )}
                    {rc.subscriptionRatio > 0 && (
                      <div className='flex items-start gap-2'>
                        <Badge dot type='warning' />
                        <Text className='text-sm'>
                          {t('好友购买订阅套餐，您可获得套餐额度的')}{' '}
                          <Text strong type='warning'>{(rc.subscriptionRatio * 100).toFixed(0)}%</Text>{' '}
                          {t('作为返利奖励')}
                        </Text>
                      </div>
                    )}
                  </>
                ) : (
                  <>
                    {rc.rebateFixedQuota > 0 && (
                      <div className='flex items-start gap-2'>
                        <Badge dot type='warning' />
                        <Text className='text-sm'>
                          {t('好友每次充值，您可获得')}{' '}
                          <Text strong type='warning'>{renderQuota(rc.rebateFixedQuota)}</Text>{' '}
                          {t('固定返利')}
                        </Text>
                      </div>
                    )}
                    {rc.subscriptionFixedQuota > 0 && (
                      <div className='flex items-start gap-2'>
                        <Badge dot type='warning' />
                        <Text className='text-sm'>
                          {t('好友购买订阅套餐，您可获得')}{' '}
                          <Text strong type='warning'>{renderQuota(rc.subscriptionFixedQuota)}</Text>{' '}
                          {t('固定返利')}
                        </Text>
                      </div>
                    )}
                  </>
                )}
                {rc.minConsume > 0 && (
                  <div className='flex items-start gap-2'>
                    <Badge dot type='tertiary' />
                    <Text type='tertiary' className='text-sm'>
                      {t('返利要求单次充值不低于')}{' '}
                      <Text strong>{renderQuota(rc.minConsume)}</Text>
                    </Text>
                  </div>
                )}
              </>
            )}
            <div className='flex items-start gap-2'>
              <Badge dot type='success' />
              <Text type='tertiary' className='text-sm'>
                {t('通过划转功能将奖励额度转入到您的账户余额中')}
              </Text>
            </div>
            <div className='flex items-start gap-2'>
              <Badge dot type='success' />
              <Text className='text-sm' strong type='success'>
                {t('邀请的好友越多，获得的奖励越多')} 🎉
              </Text>
            </div>
          </div>
        </Card>
      </Space>
    </Card>
  );
};

export default InvitationCard;
