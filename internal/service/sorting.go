package service

import orthanc_bridgev1 "github.com/tierklinik-dobersberg/apis/gen/go/tkd/orthanc_bridge/v1"

type StudyListByTime []*orthanc_bridgev1.Study

func (sl StudyListByTime) Len() int { return len(sl) }

func (sl StudyListByTime) Less(i, j int) bool {
	its := sl[i].Time.AsTime()
	jts := sl[j].Time.AsTime()

	return its.Unix() < jts.Unix()
}

func (sl StudyListByTime) Swap(i, j int) {
	sl[i], sl[j] = sl[j], sl[i]
}
