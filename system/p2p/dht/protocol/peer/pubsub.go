package peer

import (
	"fmt"
	//"github.com/33cn/chain33/client"
	"sync"

	"github.com/33cn/chain33/queue"
	"github.com/33cn/chain33/system/p2p/dht/net"
	prototypes "github.com/33cn/chain33/system/p2p/dht/protocol/types"
	p2pty "github.com/33cn/chain33/system/p2p/dht/types"
	"github.com/33cn/chain33/types"
)

type peerPubSub struct {
	*prototypes.BaseProtocol
	p2pCfg       *p2pty.P2PSubConfig
	externalAddr string
	mutex        sync.Mutex
	pubsubOp     *net.PubSub
	topicMoudle  sync.Map
	msgChan      chan interface{}
}

func (p *peerPubSub) InitProtocol(env *prototypes.P2PEnv) {
	p.P2PEnv = env
	p.p2pCfg = env.SubConfig

	pubsub, err := net.NewPubSub(p.GetP2PEnv().Ctx, p.Host)
	if err != nil {
		return
	}

	p.pubsubOp = pubsub
	p.msgChan = make(chan interface{})
	//绑定订阅事件与相关处理函数
	prototypes.RegisterEventHandler(types.EventSubTopic, p.handleSubTopic)
	//获取订阅topic列表
	prototypes.RegisterEventHandler(types.EventFetchTopics, p.handleGetTopics)
	//移除订阅主题
	prototypes.RegisterEventHandler(types.EventRemoveTopic, p.handleRemoveTopc)
	//发布消息
	prototypes.RegisterEventHandler(types.EventPubTopicMsg, p.handlePubMsg)
	go p.ReceiveChanData()
}

//处理订阅topic的请求
func (p *peerPubSub) handleSubTopic(msg *queue.Message) {
	//先检查是否已经订阅相关topic
	//接收chain33其他模块发来的请求消息
	subtopic := msg.GetData().(*types.SubTopic)
	topic := subtopic.GetTopic()
	//check topic
	moduleName := subtopic.GetModule()
	//模块名，用来收到订阅的消息后转发给对应的模块名
	if !p.pubsubOp.HasTopic(topic) {
		err := p.pubsubOp.JoinTopicAndSubTopic(topic, p.msgChan) //订阅topic
		if err != nil {
			log.Error("peerPubSub", "err", err)
			msg.Reply(p.GetQueueClient().NewMessage("", types.EventSubTopic, types.Reply{IsOk: false, Msg: []byte(err.Error())}))
			return
		}
	}

	var reply types.SubTopicReply
	reply.Status = true
	reply.Msg = fmt.Sprintf("subtopic %v success", topic)
	msg.Reply(p.GetQueueClient().NewMessage("", types.EventSubTopic, &reply))
	//存储topic关联的moduleName
	moudles, ok := p.topicMoudle.Load(topic)
	if ok {
		moudles.(map[string]bool)[moduleName] = true
	} else {
		moudles := make(map[string]bool)
		moudles[moduleName] = true
		p.topicMoudle.Store(topic, moudles)
		return
	}
	p.topicMoudle.Store(topic, moudles)
	//接收订阅的消息
}

//处理收到的数据
func (p *peerPubSub) ReceiveChanData() {
	for {
		v, ok := <-p.msgChan
		if !ok {
			log.Info("ReceiveChanData", "PubSubMsgChan", "closed")
			return
		}
		msg, ok := v.(*types.TopicData)
		if !ok {
			continue
		}
		moudles, ok := p.topicMoudle.Load(msg.Topic)
		if !ok {
			continue
		}
		for moudleName := range moudles.(map[string]bool) {
			client := p.GetQueueClient()
			newmsg := client.NewMessage(moudleName, types.EventReceiveSubData, &types.TopicData{Topic: msg.Topic, From: msg.From, Data: msg.Data}) //加入到输出通道)
			client.Send(newmsg, false)
		}

	}
}

//获取所有已经订阅的topic
func (p *peerPubSub) handleGetTopics(msg *queue.Message) {
	_, ok := msg.GetData().(*types.FetchTopicList)
	if !ok {
		msg.Reply(p.GetQueueClient().NewMessage("", types.EventFetchTopics, &types.Reply{IsOk: false, Msg: []byte("need *types.FetchTopicList")}))
		return
	}
	//获取topic列表
	topics := p.pubsubOp.GetTopics()
	var reply types.TopicList
	reply.Topics = topics
	msg.Reply(p.GetQueueClient().NewMessage("", types.EventFetchTopics, &reply))
}

//删除已经订阅的某一个topic
func (p *peerPubSub) handleRemoveTopc(msg *queue.Message) {
	v, ok := msg.GetData().(*types.RemoveTopic)
	if !ok {
		msg.Reply(p.GetQueueClient().NewMessage("", types.EventRemoveTopic, &types.Reply{IsOk: false, Msg: []byte("need *types.RemoveTopic")}))
		return
	}

	vmdoules, ok := p.topicMoudle.Load(v.GetTopic())
	if !ok || len(vmdoules.(map[string]bool)) == 0 {
		msg.Reply(p.GetQueueClient().NewMessage("", types.EventRemoveTopic, &types.RemoveTopicReply{Topic: v.GetTopic(), Status: true, Msg: "this module no sub this topic"}))
		return
	}
	modules := vmdoules.(map[string]bool)
	delete(modules, v.GetModule()) //删除消息推送的module
	if len(modules) != 0 {
		msg.Reply(p.GetQueueClient().NewMessage("", types.EventRemoveTopic, &types.RemoveTopicReply{Topic: v.GetTopic(), Status: true}))
		return
	}

	p.pubsubOp.RemoveTopic(v.GetTopic())
	var reply types.RemoveTopicReply
	reply.Topic = v.GetTopic()
	reply.Status = true
	msg.Reply(p.GetQueueClient().NewMessage("", types.EventRemoveTopic, &reply))
}

//发布Topic消息
func (p *peerPubSub) handlePubMsg(msg *queue.Message) {
	v, ok := msg.GetData().(*types.PublishTopicMsg)
	if !ok {
		msg.Reply(p.GetQueueClient().NewMessage("", types.EventPubTopicMsg, &types.Reply{IsOk: false, Msg: []byte("need *types.PublishTopicMsg")}))
		return
	}
	var isok bool = true
	var replyinfo string = "push success"
	err := p.pubsubOp.Publish(v.GetTopic(), v.GetMsg())
	if err != nil {
		//publish msg success
		isok = false
		replyinfo = err.Error()
	}
	msg.Reply(p.GetQueueClient().NewMessage("", types.EventPubTopicMsg, &types.Reply{IsOk: isok, Msg: []byte(replyinfo)}))
}
