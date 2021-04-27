package tasks

import (
	"github.com/go-pg/pg/v10"
	"github.com/pkg/errors"
	"github.com/tribalwarshelp/shared/models"
	"time"
)

type taskUpdateServerHistory struct {
	*task
}

func (t *taskUpdateServerHistory) execute(timezone string, server *models.Server) error {
	if err := t.validatePayload(server); err != nil {
		log.Debug(err)
		return nil
	}
	location, err := t.loadLocation(timezone)
	if err != nil {
		err = errors.Wrap(err, "taskUpdateServerHistory.execute")
		log.Error(err)
		return err
	}
	entry := log.WithField("key", server.Key)
	entry.Infof("taskUpdateServerHistory.execute: %s: update of the history has started...", server.Key)
	err = (&workerUpdateServerHistory{
		db:       t.db.WithParam("SERVER", pg.Safe(server.Key)),
		server:   server,
		location: location,
	}).update()
	if err != nil {
		err = errors.Wrap(err, "taskUpdateServerHistory.execute")
		entry.Error(err)
		return err
	}
	entry.Infof("taskUpdateServerHistory.execute: %s: history has been updated", server.Key)

	return nil
}

func (t *taskUpdateServerHistory) validatePayload(server *models.Server) error {
	if server == nil {
		return errors.Errorf("taskUpdateServerHistory.validatePayload: Expected *models.Server, got nil")
	}

	return nil
}

type workerUpdateServerHistory struct {
	db       *pg.DB
	server   *models.Server
	location *time.Location
}

func (w *workerUpdateServerHistory) update() error {
	var players []*models.Player
	if err := w.db.Model(&players).Where("exists = true").Select(); err != nil {
		return errors.Wrap(err, "couldn't load players")
	}

	now := time.Now().In(w.location)
	createDate := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	var ph []*models.PlayerHistory
	for _, player := range players {
		ph = append(ph, &models.PlayerHistory{
			OpponentsDefeated: player.OpponentsDefeated,
			PlayerID:          player.ID,
			TotalVillages:     player.TotalVillages,
			Points:            player.Points,
			Rank:              player.Rank,
			TribeID:           player.TribeID,
			CreateDate:        createDate,
		})
	}

	var tribes []*models.Tribe
	if err := w.db.Model(&tribes).Where("exists = true").Select(); err != nil {
		return errors.Wrap(err, "couldn't load tribes")
	}
	var th []*models.TribeHistory
	for _, tribe := range tribes {
		th = append(th, &models.TribeHistory{
			OpponentsDefeated: tribe.OpponentsDefeated,
			TribeID:           tribe.ID,
			TotalMembers:      tribe.TotalMembers,
			TotalVillages:     tribe.TotalVillages,
			Points:            tribe.Points,
			AllPoints:         tribe.AllPoints,
			Rank:              tribe.Rank,
			Dominance:         tribe.Dominance,
			CreateDate:        createDate,
		})
	}

	tx, err := w.db.Begin()
	if err != nil {
		return err
	}
	defer func(s *models.Server) {
		if err := tx.Close(); err != nil {
			log.Warn(errors.Wrapf(err, "%s: Couldn't rollback the transaction", s.Key))
		}
	}(w.server)

	if len(ph) > 0 {
		if _, err := w.db.Model(&ph).Insert(); err != nil {
			return errors.Wrap(err, "couldn't insert players history")
		}
	}

	if len(th) > 0 {
		if _, err := w.db.Model(&th).Insert(); err != nil {
			return errors.Wrap(err, "couldn't insert tribes history")
		}
	}

	if _, err := tx.Model(w.server).
		Set("history_updated_at = ?", time.Now()).
		WherePK().
		Returning("*").
		Update(); err != nil {
		return errors.Wrap(err, "couldn't update server")

	}

	return tx.Commit()
}
